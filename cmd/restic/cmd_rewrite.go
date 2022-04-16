package main

import (
	"context"
	"os"
	"path"
	"time"

	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	"github.com/spf13/cobra"
)

var cmdRewrite = &cobra.Command{
	Use:   "rewrite [flags] [snapshotID ...]",
	Short: "Rewrite existing snapshots by deleting files",
	Long: `
The "rewrite" command excludes files from existing snapshots.

By default "rewrite" will create new snapshots that will contain the same data as
source snapshots except excluded data. All metadata (time, host, tags) will be preserved
but using --add-tag option, tags can be added to new snapshots to distinguish them from source.

When no snapshot-ID is given, all snapshots matching the host, tag and path filter criteria are modified.

Please note, that this command only creates new snapshots. In order to delete
data from repository you may use the --forget and --prune flag.

If neither --add-tag nor --forget is specified, "rewrite" will work in dry-run mode.

EXIT STATUS
===========

Exit status is 0 if the command was successful, and non-zero if there was any error.
`,
	DisableAutoGenTag: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRewrite(rewriteOptions, globalOptions, args)
	},
}

// RewriteOptions collects all options for the rewrite command.
type RewriteOptions struct {
	Hosts   []string
	Paths   []string
	Tags    restic.TagLists
	AddTags restic.TagList
	Forget  bool
	Prune   bool
	DryRun  bool
	Compact bool

	Excludes                []string
	InsensitiveExcludes     []string
	ExcludeFiles            []string
	InsensitiveExcludeFiles []string
	ExcludeLargerThan       string
}

var rewriteOptions RewriteOptions

func init() {
	cmdRoot.AddCommand(cmdRewrite)

	f := cmdRewrite.Flags()
	f.StringArrayVarP(&rewriteOptions.Excludes, "exclude", "e", nil, "exclude a `pattern` (can be specified multiple times)")
	f.StringArrayVar(&rewriteOptions.InsensitiveExcludes, "iexclude", nil, "same as --exclude `pattern` but ignores the casing of filenames")
	f.StringArrayVar(&rewriteOptions.ExcludeFiles, "exclude-file", nil, "read exclude patterns from a `file` (can be specified multiple times)")
	f.StringArrayVar(&rewriteOptions.InsensitiveExcludeFiles, "iexclude-file", nil, "same as --exclude-file but ignores casing of `file`names in patterns")
	f.StringVar(&rewriteOptions.ExcludeLargerThan, "exclude-larger-than", "", "max `size` of the files to keep in snapshot (allowed suffixes: k/K, m/M, g/G, t/T)")

	f.StringArrayVarP(&rewriteOptions.Hosts, "host", "H", nil, "only consider snapshots for this `host`, when no snapshot ID is given (can be specified multiple times)")
	f.Var(&rewriteOptions.Tags, "tag", "only consider snapshots which include this `taglist`, when no snapshot-ID is given")
	f.StringArrayVar(&rewriteOptions.Paths, "path", nil, "only consider snapshots which include this (absolute) `path`, when no snapshot-ID is given")

	f.Var(&rewriteOptions.AddTags, "add-tag", "`tags` which will be added to the existing tags in the format `tag[,tag,...]` (can be specified multiple times)")
	f.BoolVarP(&rewriteOptions.DryRun, "dry-run", "n", false, "do not modify the repository, just print what would be done")
	f.BoolVarP(&rewriteOptions.Compact, "compact", "c", false, "use compact output format")
	f.BoolVarP(&rewriteOptions.Forget, "forget", "", false, "automatically run the 'forget' command if snapshots have been modified")
	f.BoolVar(&rewriteOptions.Prune, "prune", false, "automatically run the 'prune' command if snapshots have been removed")

	f.SortFlags = false
	addPruneOptions(cmdRewrite)
}

type (
	saveTreeFunction = func(context.Context, *restic.Tree) (restic.ID, error)
	rejectFunction   = func(string, *restic.Node) bool
)

func filterNode(ctx context.Context, repo restic.Repository, nodepath string, nodeID restic.ID,
	checkExclude rejectFunction, saveTreeFunc saveTreeFunction) (restic.ID, error) {
	curTree, err := repo.LoadTree(ctx, nodeID)
	if err != nil {
		return nodeID, err
	}

	debug.Log("filterNode: %s, nodeId: %s\n", nodepath, nodeID.Str())

	changed := false
	newTree := restic.NewTree(len(curTree.Nodes))
	for _, node := range curTree.Nodes {
		path := path.Join(nodepath, node.Name)
		if !checkExclude(path, node) {
			if node.Subtree == nil {
				_ = newTree.Insert(node)

				continue
			}
			newNode := node
			newID, err := filterNode(ctx, repo, path, *node.Subtree, checkExclude, saveTreeFunc)
			if err != nil {
				return nodeID, err
			}
			if newID == *node.Subtree {
				_ = newTree.Insert(node)
			} else {
				changed = true
				newNode.Subtree = new(restic.ID)
				*newNode.Subtree = newID
				_ = newTree.Insert(newNode)
			}
		} else {
			Verboseff("excluding %s\n", path)
			changed = true
		}
	}

	if changed {
		// save new tree
		newTreeID, err := saveTreeFunc(ctx, newTree)
		debug.Log("filterNode: save new tree for %s as %v\n", nodepath, newTreeID)

		return newTreeID, err
	}

	return nodeID, nil
}

func rewriteSnapshot(ctx context.Context, repo *repository.Repository, sn *restic.Snapshot, addTags restic.TagList,
	checkExclude rejectFunction, saveTreeFunc saveTreeFunction) (*restic.Snapshot, error) {
	if sn.Tree == nil {
		return nil, errors.Errorf("snapshot %v has nil tree", sn.ID())
	}

	filteredTree, err := filterNode(ctx, repo, "/", *sn.Tree, checkExclude, saveTreeFunc)
	if err != nil {
		return nil, err
	}

	if filteredTree == *sn.Tree {
		debug.Log("snapshot not touched\n")

		return nil, nil
	}

	debug.Log("snapshot modified\n")

	newsn := *sn
	newsn.Tree = &filteredTree

	// use Original as a persistent snapshot ID
	if newsn.Original == nil {
		newsn.Original = sn.ID()
	}

	if len(addTags) > 0 {
		newsn.AddTags(addTags)
	}

	return &newsn, nil
}

func collectRejectByNameFuncsForRewrite(opts RewriteOptions) (fs []RejectByNameFunc, err error) {
	// add patterns from files
	if len(opts.ExcludeFiles) > 0 {
		excludes, err := readExcludePatternsFromFiles(opts.ExcludeFiles)
		if err != nil {
			return nil, err
		}
		opts.Excludes = append(opts.Excludes, excludes...)
	}

	if len(opts.InsensitiveExcludeFiles) > 0 {
		excludes, err := readExcludePatternsFromFiles(opts.InsensitiveExcludeFiles)
		if err != nil {
			return nil, err
		}
		opts.InsensitiveExcludes = append(opts.InsensitiveExcludes, excludes...)
	}

	if len(opts.InsensitiveExcludes) > 0 {
		fs = append(fs, rejectByInsensitivePattern(opts.InsensitiveExcludes))
	}

	if len(opts.Excludes) > 0 {
		fs = append(fs, rejectByPattern(opts.Excludes))
	}

	return fs, nil
}

type nodeFileInfo struct{ n *restic.Node }

func (node nodeFileInfo) Name() string {
	return node.n.Name
}

func (node nodeFileInfo) Size() int64 {
	return int64(node.n.Size)
}

func (node nodeFileInfo) Mode() os.FileMode {
	return node.n.Mode
}

func (node nodeFileInfo) ModTime() time.Time {
	return node.n.ModTime
}

func (node nodeFileInfo) IsDir() bool {
	return node.n.Type == "dir"
}

func (node nodeFileInfo) Sys() interface{} {
	return nil
}

func collectRejectFuncsForRewrite(opts RewriteOptions) (fs []RejectFunc, err error) {
	if len(opts.ExcludeLargerThan) != 0 {
		f, err := rejectBySize(opts.ExcludeLargerThan)
		if err != nil {
			return nil, err
		}
		fs = append(fs, f)
	}

	return fs, nil
}

func runRewrite(opts RewriteOptions, gopts GlobalOptions, args []string) error {
	if err := verifyPruneOptions(&pruneOptions); err != nil {
		return err
	}
	if len(opts.Excludes) == 0 && len(opts.InsensitiveExcludes) == 0 &&
		len(opts.ExcludeFiles) == 0 && len(opts.InsensitiveExcludeFiles) == 0 &&
		opts.ExcludeLargerThan == "" {
		return errors.Fatal("Nothing to do: no excludes provided")
	}
	if !opts.DryRun && !opts.Forget && len(opts.AddTags) == 0 {
		opts.DryRun = true
		Warnf("implicit dry run: --add-tag and --forget not specified\n")
	}

	rejectFuncs, err := collectRejectFuncsForRewrite(opts)
	if err != nil {
		return err
	}

	rejectByNameFuncs, err := collectRejectByNameFuncsForRewrite(opts)
	if err != nil {
		return err
	}

	checkExclude := func(nodepath string, node *restic.Node) bool {
		for _, reject := range rejectByNameFuncs {
			if reject(nodepath) {
				return true
			}
		}

		for _, reject := range rejectFuncs {
			if reject(nodepath, nodeFileInfo{node}) {
				return true
			}
		}

		return false
	}

	repo, err := OpenRepository(gopts)
	if err != nil {
		return err
	}

	// stand-alone 'forget' command ignores --no-lock flag
	if (!gopts.NoLock || opts.Forget) && !opts.DryRun {
		// 'forget' command requires exclusive repository lock
		lock, err := lockRepository(gopts.ctx, repo, opts.Forget)
		defer unlockRepo(lock)
		if err != nil {
			return err
		}
	}

	if err = repo.LoadIndex(gopts.ctx); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(gopts.ctx)
	defer cancel()

	saveTreeFunc := repo.SaveTree
	if opts.DryRun {
		saveTreeFunc = func(ctx context.Context, tree *restic.Tree) (restic.ID, error) {
			return restic.ID{}, nil
		}
	}

	type changedSnaphot struct {
		newSn   *restic.Snapshot
		oldSnID *restic.ID
	}
	var changed []changedSnaphot
	for sn := range FindFilteredSnapshots(ctx, repo, opts.Hosts, opts.Tags, opts.Paths, args) {
		if opts.Compact {
			Verbosef("checking snapshot %s\n", sn.ID().Str())
		} else {
			Verbosef("checking snapshot %s\n", sn.String())
		}
		if newsn, err := rewriteSnapshot(ctx, repo, sn, opts.AddTags, checkExclude, saveTreeFunc); err != nil {
			Warnf("unable to rewrite snapshot %s, ignoring: %v\n", sn.ID().Str(), err)
		} else if newsn != nil {
			Verbosef("snapshot %s modified\n", sn.ID().Str())
			changed = append(changed, changedSnaphot{newsn, sn.ID()})
		}
	}

	if len(changed) == 0 {
		Verbosef("no snapshots modified\n")

		return nil
	}
	if opts.DryRun {
		Verbosef("would have modified %d snapshots\n", len(changed))

		return nil
	}

	if err = repo.Flush(ctx); err != nil {
		return err
	}

	// save the new snapshots
	Verbosef("will save %d new snapshots\n", len(changed))
	removeSnIDs := make([]string, 0, len(changed))
	for _, sn := range changed {
		id, err := repo.SaveJSONUnpacked(ctx, restic.SnapshotFile, sn.newSn)
		if err != nil {
			return err
		}
		Verboseff("snapshot %s saved as %s\n", sn.oldSnID.Str(), id.Str())
		removeSnIDs = append(removeSnIDs, sn.oldSnID.String())
	}
	Verbosef("modified %d snapshots\n", len(changed))

	// call 'forget' command
	if !opts.DryRun && opts.Forget && len(removeSnIDs) > 0 {
		forgetOptions.Prune = opts.Prune
		forgetOptions.Compact = opts.Compact
		if err := runForgetWithRepo(forgetOptions, gopts, removeSnIDs, repo); err != nil {
			return err
		}
	}

	return nil
}
