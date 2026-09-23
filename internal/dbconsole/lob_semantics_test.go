// 纯离线 LOB / 投影语义单元测试 (QA-01 拆分后保留在默认测试集)
//
// 真实 Oracle 的 LOB 集成测试已移到 lob_real_oracle_integration_test.go
// (build tag integration)。默认 `go test ./...` 不再编译它, 也就不会再
// NewManager(用户提供的目录) 去读取真实数据源并连接真实 Oracle 实例。
// 本文件只保留完全离线、不需要数据库的断言。
package dbconsole

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestLOBTokenExactNumber(t *testing.T) {
	tok, err := GenerateSignedLOBToken("s", "u", "O", "T", "C", "CLOB", "", map[string]any{"ID": json.Number("9007199254740993")}, []string{"ID"}, "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, ref, err := VerifySignedLOBToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ref.Keys["ID"]) != "9007199254740993" {
		t.Fatal("numeric key precision lost")
	}
}

func TestProjectionLeavesSQLSemanticsIntact(t *testing.T) {
	for _, s := range []string{"SELECT * FROM t WHERE id=:id", "SELECT * FROM t WHERE a>=1", "SELECT * FROM t WHERE a=1+2", "SELECT * FROM t@link", "SELECT /*+ FULL(t) */ * FROM t", "SELECT * FROM t WHERE x IN (SELECT x FROM u)", "SELECT * FROM t ORDER BY 2", "SELECT * FROM t WHERE x=q'[a'b]'"} {
		if _, ok := parseSafeSingleTableQuery(s); ok {
			t.Fatalf("unsafe rewrite: %s", s)
		}
	}
}
