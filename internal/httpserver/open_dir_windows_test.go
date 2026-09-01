//go:build windows

package httpserver

import (
	"strings"
	"testing"
)

func TestExplorerCommandsNeverUseCommandShell(t *testing.T) {
	path := `C:\tmp\release&calc\artifact.zip`
	reveal, _ := platformRevealCommand(path)
	if strings.Contains(strings.ToLower(reveal.Path), "cmd.exe") || strings.Contains(strings.ToLower(reveal.SysProcAttr.CmdLine), "cmd /c") {
		t.Fatalf("reveal command uses command shell: path=%s cmdline=%s", reveal.Path, reveal.SysProcAttr.CmdLine)
	}
	if !strings.Contains(reveal.SysProcAttr.CmdLine, `/select,"C:\tmp\release&calc\artifact.zip"`) {
		t.Fatalf("unexpected reveal command line: %s", reveal.SysProcAttr.CmdLine)
	}

	open, _ := platformOpenFolderCommand(`C:\tmp\release&calc`)
	if strings.Contains(strings.ToLower(open.Path), "cmd.exe") || strings.Contains(strings.ToLower(open.SysProcAttr.CmdLine), "cmd /c") {
		t.Fatalf("open command uses command shell: path=%s cmdline=%s", open.Path, open.SysProcAttr.CmdLine)
	}
}
