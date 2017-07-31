package manifest

import (
	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/docker/cli/cli"
	"github.com/docker/cli/cli/command"
	"github.com/docker/docker/registry"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/net/context"
)

type annotateOpts struct {
	amend bool
}

func newCreateListCommand(dockerCli command.Cli) *cobra.Command {
	opts := annotateOpts{}

	cmd := &cobra.Command{
		Use:   "create newRef manifest [manifest...]",
		Short: "Create a local manifest list for annotating and pushing to a registry",
		Args:  cli.RequiresMinArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return createManifestList(dockerCli, args, opts)
		},
	}

	flags := cmd.Flags()
	flags.BoolVarP(&opts.amend, "amend", "a", false, "Amend an existing manifest list transaction")
	return cmd
}

func createManifestList(dockerCli command.Cli, args []string, opts annotateOpts) error {
	newRef := args[0]
	targetRef, err := normalizeReference(newRef)
	if err != nil {
		return errors.Wrapf(err, "error parsing name for manifest list (%s): %v", newRef)
	}

	// TODO: why is this here?
	_, err = registry.ParseRepositoryInfo(targetRef)
	if err != nil {
		return errors.Wrapf(err, "error parsing repository name for manifest list (%s): %v", newRef)
	}

	manifestStore := dockerCli.ManifestStore()
	list, err := manifestStore.GetList(targetRef)
	if err != nil {
		return err
	}
	if len(list) > 0 && !opts.amend {
		return fmt.Errorf("refusing to continue over an existing manifest list transaction with no --amend flag")
	}

	ctx := context.Background()
	// Now create the local manifest list transaction by looking up the manifest schemas
	// for the constituent images:
	manifests := args[1:]
	logrus.Debugf("retrieving digests of images...")
	for _, manifestRef := range manifests {
		namedRef, err := normalizeReference(manifestRef)
		if err != nil {
			// TODO: wrap error?
			return err
		}

		manifest, err := getManifest(ctx, dockerCli, targetRef, namedRef)
		if err != nil {
			return err
		}
		if err := manifestStore.Save(targetRef, namedRef, manifest); err != nil {
			return err
		}
	}
	logrus.Infof("successfully started manifest list transaction for %s", targetRef.String())
	return nil
}
