package manifest

import (
	"github.com/Sirupsen/logrus"
	"github.com/docker/cli/cli"
	"github.com/docker/cli/cli/command"
	"github.com/docker/cli/cli/manifest/store"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/net/context"
)

type annotateOptions struct {
	target     string // the target manifest list name (also transaction ID)
	image      string // the manifest to annotate within the list
	variant    string // an architecture variant
	os         string
	arch       string
	osFeatures []string
}

// NewAnnotateCommand creates a new `docker manifest annotate` command
func newAnnotateCommand(dockerCli command.Cli) *cobra.Command {
	var opts annotateOptions

	cmd := &cobra.Command{
		Use:   "annotate [OPTIONS] MANIFEST_LIST MANIFEST",
		Short: "Add additional information to a local image manifest",
		Args:  cli.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.target = args[0]
			opts.image = args[1]
			return runManifestAnnotate(dockerCli, opts)
		},
	}

	flags := cmd.Flags()

	flags.StringVar(&opts.os, "os", "", "Set operating system")
	flags.StringVar(&opts.arch, "arch", "", "Set architecture")
	flags.StringSliceVar(&opts.osFeatures, "os-features", []string{}, "Set operating system feature")
	flags.StringVar(&opts.variant, "variant", "", "Set architecture variant")

	return cmd
}

func runManifestAnnotate(dockerCli command.Cli, opts annotateOptions) error {
	targetRef, err := normalizeReference(opts.target)
	if err != nil {
		return errors.Wrapf(err, "annotate: Error parsing name for manifest list (%s): %s", opts.target)
	}
	imgRef, err := normalizeReference(opts.image)
	if err != nil {
		return errors.Wrapf(err, "annotate: Error parsing name for manifest (%s): %s:", opts.image)
	}

	logrus.Debugf("beginning annotate for %s/%s", targetRef, imgRef)

	ctx := context.Background()
	manifestStore := dockerCli.ManifestStore()
	imageManfiest, err := manifestStore.Get(targetRef, imgRef)
	switch {
	case store.IsNotFound(err):
		imageManfiest, err = getManifest(ctx, dockerCli, targetRef, imgRef)
		if err != nil {
			return err
		}
		if err := manifestStore.Save(targetRef, imgRef, imageManfiest); err != nil {
			return err
		}
	case err != nil:
		return err
	}

	// Update the mf
	if opts.os != "" {
		imageManfiest.Platform.OS = opts.os
	}
	if opts.arch != "" {
		imageManfiest.Platform.Architecture = opts.arch
	}
	for _, osFeature := range opts.osFeatures {
		imageManfiest.Platform.OSFeatures = appendIfUnique(imageManfiest.Platform.OSFeatures, osFeature)
	}
	if opts.variant != "" {
		imageManfiest.Platform.Variant = opts.variant
	}

	if !isValidOSArch(imageManfiest.Platform.OS, imageManfiest.Platform.Architecture) {
		return errors.Errorf("manifest entry for image has unsupported os/arch combination: %s/%s", opts.os, opts.arch)
	}
	return manifestStore.Save(targetRef, imgRef, imageManfiest)
}

func appendIfUnique(list []string, str string) []string {
	for _, s := range list {
		if s == str {
			return list
		}
	}
	return append(list, str)
}
