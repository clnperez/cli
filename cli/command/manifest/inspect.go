package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/docker/cli/cli"
	"github.com/docker/cli/cli/command"
	"github.com/docker/cli/cli/manifest/fetcher"
	"github.com/docker/distribution/manifest/manifestlist"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/registry"
	"github.com/spf13/cobra"
	"golang.org/x/net/context"
)

type inspectOptions struct {
	remote  string
	verbose bool
}

// NewInspectCommand creates a new `docker manifest inspect` command
func newInspectCommand(dockerCli command.Cli) *cobra.Command {
	var opts inspectOptions

	cmd := &cobra.Command{
		Use:   "inspect [OPTIONS] NAME[:TAG]",
		Short: "Display an image manifest, or a remote manifest list",
		Args:  cli.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.remote = args[0]
			return runListInspect(dockerCli, opts)
		},
	}

	flags := cmd.Flags()
	flags.BoolVarP(&opts.verbose, "verbose", "v", false, "Output additional info including layers and platform")
	return cmd
}

func runListInspect(dockerCli command.Cli, opts inspectOptions) error {
	namedRef, err := normalizeReference(opts.remote)
	if err != nil {
		return err
	}

	localManifestList, err := dockerCli.ManifestStore().GetList(namedRef)
	if err == nil {
		return printManifestList(dockerCli, namedRef, localManifestList, opts)
	}

	ctx := context.Background()
	imgInspect, err := getRemoteManifestOrManifestList(ctx, dockerCli, namedRef)
	if err != nil {
		return err
	}
	if len(imgInspect) == 1 {
		return printManifest(dockerCli, imgInspect[0], opts)
	}
	return printManifestList(dockerCli, namedRef, imgInspect, opts)
}

func printManifest(dockerCli command.Cli, manifest fetcher.ImgManifestInspect, opts inspectOptions) error {
	buffer := new(bytes.Buffer)
	if !opts.verbose {
		err := json.Indent(buffer, manifest.CanonicalJSON, "", "\t")
		if err != nil {
			return err
		}
		fmt.Fprintln(dockerCli.Out(), buffer.String())
		return nil
	}
	jsonBytes, err := json.MarshalIndent(manifest, "", "\t")
	if err != nil {
		return err
	}
	dockerCli.Out().Write(append(jsonBytes, '\n'))
	return nil
}

func printManifestList(dockerCli command.Cli, namedRef reference.Named, list []fetcher.ImgManifestInspect, opts inspectOptions) error {
	if !opts.verbose {
		targetRepo, err := registry.ParseRepositoryInfo(namedRef)
		if err != nil {
			return err
		}

		manifests := []manifestlist.ManifestDescriptor{}
		// More than one response. This is a manifest list.
		for _, img := range list {
			mfd, _, err := buildManifestObj(targetRepo, img)
			if err != nil {
				return fmt.Errorf("error assembling ManifestDescriptor")
			}
			manifests = append(manifests, mfd)
		}
		deserializedML, err := manifestlist.FromDescriptors(manifests)
		if err != nil {
			return err
		}
		jsonBytes, err := deserializedML.MarshalJSON()
		if err != nil {
			return err
		}
		fmt.Fprintln(dockerCli.Out(), string(jsonBytes))
		return nil
	}
	jsonBytes, err := json.MarshalIndent(list, "", "\t")
	if err != nil {
		return err
	}
	dockerCli.Out().Write(append(jsonBytes, '\n'))
	return nil
}
