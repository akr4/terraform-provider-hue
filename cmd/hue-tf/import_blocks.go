package main

import (
	"context"
	"io"
)

func importBlocksCommand(ctx context.Context, args []string, out io.Writer, deps dependencies) error {
	// Compatibility for scripts using the former definition-only import workflow.
	if len(args) > 0 && args[0] == "--new" {
		return pullNew(ctx, args[1:], out, deps)
	}
	return importBlocks(ctx, args, out, deps)
}
