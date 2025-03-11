package servermanager

import (
	"bufio"
	"context"
	"os/exec"
)

// This file ensures that all required imports are available for the package.
// Go doesn't have a simple way to "import" modules from one file to another
// within the same package, so this ensures all necessary types and functions
// from external packages are available throughout the package.

// Ensures these imports are available
var (
	_ = bufio.NewScanner
	_ = context.Background
	_ = exec.Command
)
