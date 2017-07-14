package manifest

import (
	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/docker/cli/cli"
	"github.com/docker/cli/cli/command"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/registry"
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

	// Just do some basic verification here, and leave the rest for when the user pushes the list
	newRef := args[0]
	targetRef, err := reference.ParseNormalizedNamed(newRef)
	if err != nil {
		return errors.Wrapf(err, "error parsing name for manifest list (%s): %v", newRef)
	}
	_, err = registry.ParseRepositoryInfo(targetRef)
	if err != nil {
		return errors.Wrapf(err, "error parsing repository name for manifest list (%s): %v", newRef)
	}

	// Check locally for this list transaction before proceeding
	if _, isDigested := targetRef.(reference.Canonical); !isDigested {
		targetRef = reference.TagNameOnly(targetRef)
	}
	manifestFiles, err := getListFilenames(makeFilesafeName(targetRef.String()))
	if err != nil {
		return err
	}
	if len(manifestFiles) > 0 && !opts.amend {
		return fmt.Errorf("refusing to continue over an existing manifest list transaction with no --amend flag")
	}

	// Now create the local manifest list transaction by looking up the manifest schemas
	// for the constituent images:
	manifests := args[1:]
	logrus.Debugf("retrieving digests of images...")
	for _, manifestRef := range manifests {
		namedRef, err := reference.ParseNormalizedNamed(manifestRef)
		if err != nil {
			// TODO: wrap error?
			return err
		}

		mfstData, _, err := getImageData(dockerCli, namedRef, targetRef.String(), false)
		if err != nil {
			return err
		}

		if len(mfstData) > 1 {
			// too many responses--can only happen if a manifest list was returned for the name lookup
			return fmt.Errorf("manifest lists cannot embed another manifest list")
		}

	}
	logrus.Infof("successfully started manifest list transaction for %s", targetRef.String())
	return nil
}
