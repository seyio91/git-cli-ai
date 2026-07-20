package cli

import (
	"context"

	"github.com/seyio91/git-cli-ai/internal/ai"
	gitrepo "github.com/seyio91/git-cli-ai/internal/git"
	"github.com/seyio91/git-cli-ai/internal/memory"
)

// memoryContext loads the memory project this repository is pinned to. Every
// failure — no marker, no memory tree, a project that does not exist — yields a
// zero Context, because being unpinned is the ordinary case and must leave the
// command behaving exactly as it does without one.
func memoryContext(ctx context.Context, repo gitrepo.Repository) memory.Context {
	root, err := repo.Root(ctx)
	if err != nil {
		return memory.Context{}
	}

	project, ok := memory.Discover(root)
	if !ok {
		return memory.Context{}
	}
	return memory.Load(project)
}

// withMemory appends the memory blocks to a command's own context blocks. The
// command's blocks come first: they describe the change being made, which is
// more specific than the task it belongs to.
func withMemory(blocks []ai.ContextBlock, mem memory.Context) []ai.ContextBlock {
	return append(blocks, mem.Blocks...)
}
