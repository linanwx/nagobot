package cmd

import "os/exec"

func removeQuarantine(path string) {
	_ = exec.Command("xattr", "-d", "com.apple.quarantine", path).Run()
	// Ad-hoc sign so macOS doesn't SIGKILL unsigned downloaded binaries.
	_ = exec.Command("codesign", "--sign", "-", "--force", path).Run()
}
