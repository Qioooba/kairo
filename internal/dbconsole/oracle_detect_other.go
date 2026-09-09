//go:build !windows

package dbconsole

import (
	"os"
	"path/filepath"
	"strings"
)

// DetectSystemOracleClients 在非 Windows 系统上的实现（Linux / macOS）
func DetectSystemOracleClients(source Source) (candidates []OracleCandidateClient, plsqlDetected bool, plsqlBitness string, plsqlDetail string) {
	seenPaths := make(map[string]struct{})
	addCandidate := func(path, label string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		norm := filepath.Clean(path)
		if _, exists := seenPaths[norm]; exists {
			return
		}
		seenPaths[norm] = struct{}{}
		if c := EvaluateCandidate(path, label); c != nil {
			candidates = append(candidates, *c)
		}
	}

	if strings.TrimSpace(source.OracleLibDir) != "" {
		addCandidate(source.OracleLibDir, "数据源显式指定 (OracleLibDir)")
	}

	if v := strings.TrimSpace(os.Getenv("ORACLE_HOME")); v != "" {
		addCandidate(v, "环境变量 ORACLE_HOME")
		addCandidate(filepath.Join(v, "lib"), "环境变量 ORACLE_HOME/lib")
	}
	if v := strings.TrimSpace(os.Getenv("LD_LIBRARY_PATH")); v != "" {
		for _, p := range filepath.SplitList(v) {
			addCandidate(p, "LD_LIBRARY_PATH")
		}
	}
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		addCandidate(p, "PATH")
	}

	for _, d := range []string{
		"/usr/lib/oracle/21/client64/lib",
		"/usr/lib/oracle/19.20/client64/lib",
		"/usr/lib/oracle/19.19/client64/lib",
		"/usr/lib/oracle/19.10/client64/lib",
		"/usr/lib/oracle/18.5/client64/lib",
		"/usr/lib/oracle/12.2/client64/lib",
		"/usr/lib/oracle/11.2/client64/lib",
		"/opt/oracle/instantclient",
	} {
		addCandidate(d, "标准 Instant Client 路径")
	}

	return candidates, false, "", ""
}
