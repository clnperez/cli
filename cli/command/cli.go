package command

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/docker/cli/cli"
	"github.com/docker/cli/cli/config"
	cliconfig "github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	cliflags "github.com/docker/cli/cli/flags"
	manifeststore "github.com/docker/cli/cli/manifest/store"
	registryclient "github.com/docker/cli/cli/registry/client"
	"github.com/docker/cli/cli/trust"
	dopts "github.com/docker/cli/opts"
	"github.com/docker/docker/api"
	"github.com/docker/docker/api/types"
	registrytypes "github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/sockets"
	"github.com/docker/go-connections/tlsconfig"
	"github.com/docker/notary"
	notaryclient "github.com/docker/notary/client"
	"github.com/docker/notary/passphrase"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/net/context"
)

// Streams is an interface which exposes the standard input and output streams
type Streams interface {
	In() *InStream
	Out() *OutStream
	Err() io.Writer
}

// Cli represents the docker command line client.
type Cli interface {
	Client() client.APIClient
	Out() *OutStream
	Err() io.Writer
	In() *InStream
	SetIn(in *InStream)
	ConfigFile() *configfile.ConfigFile
	ServerInfo() ServerInfo
	NotaryClient(imgRefAndAuth trust.ImageRefAndAuth, actions []string) (notaryclient.Repository, error)
	ManifestStore() manifeststore.Store
	RegistryClient(bool) registryclient.RegistryClient
}

// DockerCli is an instance the docker command line client.
// Instances of the client can be returned from NewDockerCli.
type DockerCli struct {
	configFile     *configfile.ConfigFile
	in             *InStream
	out            *OutStream
	err            io.Writer
	client         client.APIClient
	defaultVersion string
	server         ServerInfo
}

// DefaultVersion returns api.defaultVersion or DOCKER_API_VERSION if specified.
func (cli *DockerCli) DefaultVersion() string {
	return cli.defaultVersion
}

// Client returns the APIClient
func (cli *DockerCli) Client() client.APIClient {
	return cli.client
}

// Out returns the writer used for stdout
func (cli *DockerCli) Out() *OutStream {
	return cli.out
}

// Err returns the writer used for stderr
func (cli *DockerCli) Err() io.Writer {
	return cli.err
}

// SetIn sets the reader used for stdin
func (cli *DockerCli) SetIn(in *InStream) {
	cli.in = in
}

// In returns the reader used for stdin
func (cli *DockerCli) In() *InStream {
	return cli.in
}

// ShowHelp shows the command help.
func ShowHelp(err io.Writer) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cmd.SetOutput(err)
		cmd.HelpFunc()(cmd, args)
		return nil
	}
}

// ConfigFile returns the ConfigFile
func (cli *DockerCli) ConfigFile() *configfile.ConfigFile {
	return cli.configFile
}

// ServerInfo returns the server version details for the host this client is
// connected to
func (cli *DockerCli) ServerInfo() ServerInfo {
	return cli.server
}

// ManifestStore returns a store for local manifests
func (cli *DockerCli) ManifestStore() manifeststore.Store {
	// TODO: support override default location from config file
	return manifeststore.NewStore(filepath.Join(config.Dir(), "manifests"))
}

// RegistryClient returns a client for communicating with a Docker distribution
// registry
func (cli *DockerCli) RegistryClient(allowInsecure bool) registryclient.RegistryClient {
	resolver := func(ctx context.Context, index *registrytypes.IndexInfo) types.AuthConfig {
		return ResolveAuthConfig(ctx, cli, index)
	}
	return registryclient.NewRegistryClient(resolver, UserAgent(), allowInsecure)
}

// Initialize the dockerCli runs initialization that must happen after command
// line flags are parsed.
func (cli *DockerCli) Initialize(opts *cliflags.ClientOptions) error {
	cli.configFile = cliconfig.LoadDefaultConfigFile(cli.err)

	var err error
	cli.client, err = NewAPIClientFromFlags(opts.Common, cli.configFile)
	if tlsconfig.IsErrEncryptedKey(err) {
		passRetriever := passphrase.PromptRetrieverWithInOut(cli.In(), cli.Out(), nil)
		newClient := func(password string) (client.APIClient, error) {
			opts.Common.TLSOptions.Passphrase = password
			return NewAPIClientFromFlags(opts.Common, cli.configFile)
		}
		cli.client, err = getClientWithPassword(passRetriever, newClient)
	}
	if err != nil {
		return err
	}
	cli.initializeFromClient()
	return nil
}

func (cli *DockerCli) initializeFromClient() {
	cli.defaultVersion = cli.client.ClientVersion()

	ping, err := cli.client.Ping(context.Background())
	if err != nil {
		// Default to true if we fail to connect to daemon
		cli.server = ServerInfo{HasExperimental: true}

		if ping.APIVersion != "" {
			cli.client.NegotiateAPIVersionPing(ping)
		}
		return
	}

	cli.server = ServerInfo{
		HasExperimental: ping.Experimental,
		OSType:          ping.OSType,
	}
	cli.client.NegotiateAPIVersionPing(ping)
}

func getClientWithPassword(passRetriever notary.PassRetriever, newClient func(password string) (client.APIClient, error)) (client.APIClient, error) {
	for attempts := 0; ; attempts++ {
		passwd, giveup, err := passRetriever("private", "encrypted TLS private", false, attempts)
		if giveup || err != nil {
			return nil, errors.Wrap(err, "private key is encrypted, but could not get passphrase")
		}

		apiclient, err := newClient(passwd)
		if !tlsconfig.IsErrEncryptedKey(err) {
			return apiclient, err
		}
	}
}

// NotaryClient provides a Notary Repository to interact with signed metadata for an image
func (cli *DockerCli) NotaryClient(imgRefAndAuth trust.ImageRefAndAuth, actions []string) (notaryclient.Repository, error) {
	return trust.GetNotaryRepository(cli.In(), cli.Out(), UserAgent(), imgRefAndAuth.RepoInfo(), imgRefAndAuth.AuthConfig(), actions...)
}

// ServerInfo stores details about the supported features and platform of the
// server
type ServerInfo struct {
	HasExperimental bool
	OSType          string
}

// NewDockerCli returns a DockerCli instance with IO output and error streams set by in, out and err.
func NewDockerCli(in io.ReadCloser, out, err io.Writer) *DockerCli {
	return &DockerCli{in: NewInStream(in), out: NewOutStream(out), err: err}
}

// NewAPIClientFromFlags creates a new APIClient from command line flags
func NewAPIClientFromFlags(opts *cliflags.CommonOptions, configFile *configfile.ConfigFile) (client.APIClient, error) {
	host, err := getServerHost(opts.Hosts, opts.TLSOptions)
	if err != nil {
		return &client.Client{}, err
	}

	customHeaders := configFile.HTTPHeaders
	if customHeaders == nil {
		customHeaders = map[string]string{}
	}
	customHeaders["User-Agent"] = UserAgent()

	verStr := api.DefaultVersion
	if tmpStr := os.Getenv("DOCKER_API_VERSION"); tmpStr != "" {
		verStr = tmpStr
	}

	httpClient, err := newHTTPClient(host, opts.TLSOptions)
	if err != nil {
		return &client.Client{}, err
	}

	return client.NewClient(host, verStr, httpClient, customHeaders)
}

func getServerHost(hosts []string, tlsOptions *tlsconfig.Options) (host string, err error) {
	switch len(hosts) {
	case 0:
		host = os.Getenv("DOCKER_HOST")
	case 1:
		host = hosts[0]
	default:
		return "", errors.New("Please specify only one -H")
	}

	host, err = dopts.ParseHost(tlsOptions != nil, host)
	return
}

func newHTTPClient(host string, tlsOptions *tlsconfig.Options) (*http.Client, error) {
	if tlsOptions == nil {
		// let the api client configure the default transport.
		return nil, nil
	}
	opts := *tlsOptions
	opts.ExclusiveRootPools = true
	config, err := tlsconfig.Client(opts)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		TLSClientConfig: config,
		DialContext: (&net.Dialer{
			KeepAlive: 30 * time.Second,
			Timeout:   30 * time.Second,
		}).DialContext,
	}
	proto, addr, _, err := client.ParseHost(host)
	if err != nil {
		return nil, err
	}

	sockets.ConfigureTransport(tr, proto, addr)

	return &http.Client{
		Transport:     tr,
		CheckRedirect: client.CheckRedirect,
	}, nil
}

// UserAgent returns the user agent string used for making API requests
func UserAgent() string {
	return "Docker-Client/" + cli.Version + " (" + runtime.GOOS + ")"
}
