package manifest

import (
	"github.com/Sirupsen/logrus"
	"github.com/docker/cli/cli/command"
	"github.com/docker/cli/cli/manifest/fetcher"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/registry"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

func getManifest(ctx context.Context, dockerCli command.Cli, listRef, namedRef reference.Named) (fetcher.ImgManifestInspect, error) {
	data, err := dockerCli.ManifestStore().Get(listRef, namedRef)
	switch {
	case err != nil:
		return fetcher.ImgManifestInspect{}, err
	case data != nil:
		return *data, nil
	}
	return getRemoteManifest(ctx, dockerCli, namedRef)
}

func getRemoteManifest(ctx context.Context, dockerCli command.Cli, namedRef reference.Named) (fetcher.ImgManifestInspect, error) {
	opts, endpoints, err := newFetchOptionsForReference(ctx, dockerCli, namedRef)
	if err != nil {
		return fetcher.ImgManifestInspect{}, err
	}

	var result fetcher.ImgManifestInspect
	fetchManifestOnly := func(endpoint registry.APIEndpoint) (bool, error) {
		logrus.Debugf("Trying to fetch image manifest of %s repository from %s %s", opts.NamedRef, endpoint.URL, endpoint.Version)
		var err error
		result, err = fetcher.FetchManifest(ctx, opts)
		return result.RefName != "", err
	}

	err = iterateEndpoints(endpoints, fetchManifestOnly)
	return result, err
}

func newFetchOptionsForReference(ctx context.Context, dockerCli command.Cli, namedRef reference.Named) (fetcher.FetchOptions, []registry.APIEndpoint, error) {
	repoInfo, err := registry.ParseRepositoryInfo(namedRef)
	if err != nil {
		return fetcher.FetchOptions{}, nil, err
	}
	registryService := registry.NewService(registry.ServiceOptions{})

	// a list of registry.APIEndpoint, which could be mirrors, etc., of locally-configured
	// repo endpoints. The list will be ordered by priority (v2, https, v1).
	endpoints, err := registryService.LookupPullEndpoints(reference.Domain(repoInfo.Name))
	if err != nil {
		return fetcher.FetchOptions{}, nil, err
	}
	logrus.Debugf("manifest pull: endpoints: %v", endpoints)

	return fetcher.FetchOptions{
		AuthConfig: command.ResolveAuthConfig(ctx, dockerCli, repoInfo.Index),
		RepoInfo:   repoInfo,
		NamedRef:   namedRef,
	}, endpoints, nil
}

func getRemoteManifestOrManifestList(ctx context.Context, dockerCli command.Cli, namedRef reference.Named) ([]fetcher.ImgManifestInspect, error) {
	opts, endpoints, err := newFetchOptionsForReference(ctx, dockerCli, namedRef)
	if err != nil {
		return nil, err
	}

	result := []fetcher.ImgManifestInspect{}
	fetchAny := func(endpoint registry.APIEndpoint) (bool, error) {
		logrus.Debugf("Trying to fetch image manifest of %s repository from %s %s", opts.NamedRef, endpoint.URL, endpoint.Version)
		foundImages, err := fetcher.Fetch(ctx, opts)
		return len(foundImages) > 0, err
	}

	if err = iterateEndpoints(endpoints, fetchAny); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, errors.Errorf("no endpoints found for %s", namedRef)
	}
	return result, nil
}

func iterateEndpoints(endpoints []registry.APIEndpoint, each func(endpoint registry.APIEndpoint) (bool, error)) error {
	confirmedTLSRegistries := make(map[string]bool)
	for _, endpoint := range endpoints {
		if endpoint.Version == registry.APIVersion1 {
			logrus.Debugf("Skipping v1 endpoint %s", endpoint.URL)
			continue
		}

		if endpoint.URL.Scheme != "https" {
			if _, confirmedTLS := confirmedTLSRegistries[endpoint.URL.Host]; confirmedTLS {
				logrus.Debugf("Skipping non-TLS endpoint %s for host/port that appears to use TLS", endpoint.URL)
				continue
			}
		}

		done, err := each(endpoint)
		if err != nil {
			// Can a manifest fetch be cancelled? I don't think so...
			if _, ok := err.(fetcher.RecoverableError); ok {
				if endpoint.URL.Scheme == "https" {
					confirmedTLSRegistries[endpoint.URL.Host] = true
				}
				continue
			}
			return err
		}
		if done {
			return nil
		}
	}
	return nil
}
