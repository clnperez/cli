package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/docker/cli/cli"
	"github.com/docker/cli/cli/command"
	"github.com/docker/distribution/manifest/manifestlist"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/registry"
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
		Short: "Display an image's manifest, or a remote manifest list.",
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

	// Get the data and then format it
	var (
		imgInspect []ImgManifestInspect
		prettyJSON bytes.Buffer
	)

	named, err := reference.ParseNormalizedNamed(opts.remote)
	if err != nil {
		return err
	}
	targetRepo, err := registry.ParseRepositoryInfo(named)
	if err != nil {
		return err
	}

	if imgInspect, _, err = getImageData(dockerCli, named.String(), "", true); err != nil {
		return err
	}
	// output basic informative details about the image
	if len(imgInspect) == 1 {
		// this is a basic single manifest
		err = json.Indent(&prettyJSON, imgInspect[0].CanonicalJSON, "", "    ")
		if err != nil {
			return err
		}
		fmt.Fprintln(dockerCli.Out(), prettyJSON.String())
		if !opts.verbose {
			return nil
		}
		mfd, _, err := buildManifestObj(targetRepo, imgInspect[0])
		if err != nil {
			return err
		}
		jsonBytes, err := json.MarshalIndent(mfd, "", "\t")
		if err != nil {
			return err
		}
		fmt.Fprintln(dockerCli.Out(), string(jsonBytes))
		return nil
	}

	manifests := []manifestlist.ManifestDescriptor{}
	// More than one response. This is a manifest list.
	for _, img := range imgInspect {
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
	if !opts.verbose {
		return nil
	}
	for _, img := range imgInspect {
		prettyJSON.Reset()
		err = json.Indent(&prettyJSON, img.CanonicalJSON, "", "    ")
		if err != nil {
			return err
		}
		fmt.Fprintln(dockerCli.Out(), prettyJSON.String())
	}
	return nil
}
