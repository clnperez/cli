package client

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/docker/cli/cli/config/configfile"
	"github.com/docker/distribution/reference"
	"github.com/docker/distribution/registry/client/auth"
	"github.com/docker/distribution/registry/client/transport"
	authtypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/pkg/homedir"
	"github.com/docker/docker/registry"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type repositoryEndpoint struct {
	info     *registry.RepositoryInfo
	endpoint registry.APIEndpoint
}

// Name returns the repository name
func (r repositoryEndpoint) Name() string {
	repoName := r.info.Name.Name()
	// If endpoint does not support CanonicalName, use the RemoteName instead
	if r.endpoint.TrimHostname {
		repoName = reference.Path(r.info.Name)
	}
	return repoName
}

// BaseURL returns the endpoint url
func (r repositoryEndpoint) BaseURL() string {
	return r.endpoint.URL.String()
}

func newDefaultRepositoryEndpoint(ref reference.Named) (repositoryEndpoint, error) {
	repoInfo, err := registry.ParseRepositoryInfo(ref)
	if err != nil {
		return repositoryEndpoint{}, err
	}
	endpoint, err := getDefaultEndpointFromRepoInfo(repoInfo)
	if err != nil {
		return repositoryEndpoint{}, err
	}
	return repositoryEndpoint{info: repoInfo, endpoint: endpoint}, nil
}

func getDefaultEndpointFromRepoInfo(repoInfo *registry.RepositoryInfo) (registry.APIEndpoint, error) {
	var err error

	options := registry.ServiceOptions{}
	// TODO: get list of InsecureRegistries from somewhere. Either from the engine
	// or maybe add it to the client config (but that would dupliate the list)
	// options.InsecureRegistries = ...?

	// does what we had before still work? if so, change to a flag on push. (--skip-cert-check?)
	// By default (unless deprecated), loopback (IPv4 at least...) is automatically added as an insecure registry.
	options.InsecureRegistries, err = loadLocalInsecureRegistries()
	if err != nil {
		return registry.APIEndpoint{}, err
	}
	registryService := registry.NewService(options)
	endpoints, err := registryService.LookupPushEndpoints(reference.Domain(repoInfo.Name))
	if err != nil {
		return registry.APIEndpoint{}, err
	}
	// Default to the highest priority endpoint to return
	endpoint := endpoints[0]
	if !repoInfo.Index.Secure {
		for _, ep := range endpoints {
			if ep.URL.Scheme == "http" {
				endpoint = ep
			}
		}
	}
	return endpoint, nil
}

// getHTTPTransport builds a transport for use in communicating with a registry
func getHTTPTransport(authConfig authtypes.AuthConfig, endpoint registry.APIEndpoint, repoName string, userAgent string) (http.RoundTripper, error) {
	// get the http transport, this will be used in a client to upload manifest
	base := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		Dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}).Dial,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     endpoint.TLSConfig,
		DisableKeepAlives:   true,
	}

	modifiers := registry.DockerHeaders(userAgent, http.Header{})
	authTransport := transport.NewTransport(base, modifiers...)
	challengeManager, confirmedV2, err := registry.PingV2Registry(endpoint.URL, authTransport)
	if err != nil {
		return nil, errors.Wrap(err, "error pinging v2 registry")
	}
	if !confirmedV2 {
		return nil, fmt.Errorf("unsupported registry version")
	}
	if authConfig.RegistryToken != "" {
		passThruTokenHandler := &existingTokenHandler{token: authConfig.RegistryToken}
		modifiers = append(modifiers, auth.NewAuthorizer(challengeManager, passThruTokenHandler))
	} else {
		creds := registry.NewStaticCredentialStore(&authConfig)
		tokenHandler := auth.NewTokenHandler(authTransport, creds, repoName, "*")
		basicHandler := auth.NewBasicHandler(creds)
		modifiers = append(modifiers, auth.NewAuthorizer(challengeManager, tokenHandler, basicHandler))
	}
	return transport.NewTransport(base, modifiers...), nil
}

// RepoNameForReference returns the repository name from a reference
func RepoNameForReference(ref reference.Named) (string, error) {
	repo, err := newDefaultRepositoryEndpoint(ref)
	if err != nil {
		return "", err
	}
	return repo.Name(), nil
}

func loadLocalInsecureRegistries() ([]string, error) {
	insecureRegistries := []string{}
	// Check $HOME/.docker/config.json. There may be mismatches between what the user has in their
	// local config and what the daemon they're talking to allows, but we can be okay with that.
	userHome, err := homedir.GetStatic()
	if err != nil {
		return []string{}, fmt.Errorf("Manifest create: lookup local insecure registries: Unable to retreive $HOME")
	}

	jsonData, err := ioutil.ReadFile(fmt.Sprintf("%s/.docker/config.json", userHome))
	if err != nil {
		if !os.IsNotExist(err) {
			return []string{}, fmt.Errorf("Manifest create: Unable to read $HOME/.docker/config.json: %s", err)
		}
		// If the file just doesn't exist, no insecure registries were specified.
		logrus.Debug("Manifest: No insecure registries were specified via $HOME/.docker/config.json")
		return []string{}, nil
	}

	if jsonData != nil {
		cf := configfile.ConfigFile{}
		if err := json.Unmarshal(jsonData, &cf); err != nil {
			logrus.Debugf("Manifest create: Unable to unmarshal insecure registries from $HOME/.docker/config.json: %s", err)
			return []string{}, nil
		}
		if cf.InsecureRegistries == nil {
			return []string{}, nil
		}
		// @TODO: Add tests for a) specifying in config.json, b) invalid entries
		for _, reg := range cf.InsecureRegistries {
			if err := net.ParseIP(reg); err == nil {
				insecureRegistries = append(insecureRegistries, reg)
			} else if _, _, err := net.ParseCIDR(reg); err == nil {
				insecureRegistries = append(insecureRegistries, reg)
			} else if ips, err := net.LookupHost(reg); err == nil {
				insecureRegistries = append(insecureRegistries, ips...)
			} else {
				return []string{}, fmt.Errorf("Manifest create: Invalid registry (%s) specified in ~/.docker/config.json: %s", reg, err)
			}
		}
	}

	return insecureRegistries, nil
}

type existingTokenHandler struct {
	token string
}

func (th *existingTokenHandler) AuthorizeRequest(req *http.Request, params map[string]string) error {
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", th.token))
	return nil
}

func (th *existingTokenHandler) Scheme() string {
	return "bearer"
}
