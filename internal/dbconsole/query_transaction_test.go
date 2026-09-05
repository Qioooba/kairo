package dbconsole

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExecuteStatementRejectsStatelessTransactionControl(t *testing.T) {
	manager := &Manager{}
	for _, statement := range []string{"COMMIT", "ROLLBACK"} {
		info, err := ValidateSQL(KindOracle, statement)
		if err != nil {
			t.Fatalf("classify %s: %v", statement, err)
		}
		_, err = manager.ExecuteStatement(context.Background(), Source{Kind: KindOracle}, statement, info, nil)
		if err == nil || !strings.Contains(err.Error(), "无状态请求") {
			t.Fatalf("%s should explain stateless transaction semantics, got %v", statement, err)
		}
	}
}

func TestRetryableQueryFailureIncludesOracleDDLReadRace(t *testing.T) {
	if !isRetryableQueryFailure(errors.New("ORA-01466: unable to read data - table definition has changed")) {
		t.Fatal("ORA-01466 should reopen the pool and retry once")
	}
	if isRetryableQueryFailure(errors.New("ORA-00942: table or view does not exist")) {
		t.Fatal("ordinary SQL errors must not be retried")
	}
}
