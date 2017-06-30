package manifest

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/docker/cli/cli"
	"github.com/docker/cli/cli/command"
	"github.com/docker/cli/cli/manifest/saver"
)

type saveOpts struct {
	output_name  string
	use_archives bool
}

func newSaveListCommand(dockerCli command.Cli) *cobra.Command {

	opts := saveOpts{}

	cmd := &cobra.Command{
		Use:   "save",
		Short: "Save a manifest list's images to a multi-arch bundle",
		Args:  cli.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return saveManifestList(dockerCli, opts, args)
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&opts.output_name, "output", "o", "manifest-save.tar", "file to contain all image bundles from a manifest list")
	flags.BoolVarP(&opts.use_archives, "use-archives", "a", true, "whether the arguments provided are archive bundles, not image names")

	return cmd
}

func saveManifestList(dockerCli command.Cli, opts saveOpts, args []string) error {

	fmt.Println("import keeper")
	if opts.use_archives {
		return saver.ManifestSaveFromArchives(opts.output_name, args)
	}

	return nil
}
