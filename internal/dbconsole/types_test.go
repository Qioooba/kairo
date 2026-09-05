package dbconsole

import (
	"encoding/json"
	"testing"
)

func TestSourceSecurityDefaultsAndExplicitWritableJSON(t *testing.T) {
	var omitted Source
	if err := json.Unmarshal([]byte(`{"kind":"mysql"}`), &omitted); err != nil {
		t.Fatal(err)
	}
	omitted.Defaults()
	if !omitted.ReadOnly || omitted.Environment != "production" || omitted.MutationAllowed() {
		t.Fatalf("omitted security policy must fail closed: %+v", omitted)
	}
	var nullPolicy Source
	if err := json.Unmarshal([]byte(`{"kind":"mysql","read_only":null}`), &nullPolicy); err != nil {
		t.Fatal(err)
	}
	nullPolicy.Defaults()
	if !nullPolicy.ReadOnly || nullPolicy.MutationAllowed() {
		t.Fatalf("null read_only must fail closed: %+v", nullPolicy)
	}

	var explicit Source
	if err := json.Unmarshal([]byte(`{"kind":"mysql","read_only":false,"environment":"development"}`), &explicit); err != nil {
		t.Fatal(err)
	}
	explicit.Defaults()
	if explicit.ReadOnly || !explicit.MutationAllowed() || explicit.Environment != "development" {
		t.Fatalf("explicit writable policy was not preserved: %+v", explicit)
	}
}

func TestRedisTopologyValidation(t *testing.T) {
	source := Source{ID: "r1", Name: "cache", Kind: KindRedis, Host: "127.0.0.1", Port: 6379, RedisMode: "cluster", RedisDB: 1, MaxResultBytes: 1 << 20}
	source.Defaults()
	if err := source.Validate(); err == nil {
		t.Fatal("cluster with DB 1 should fail")
	}
	source.RedisDB = 0
	source.RedisNodes = []string{"127.0.0.1:6380", "bad"}
	if err := source.Validate(); err == nil {
		t.Fatal("bad node should fail")
	}
	source.RedisNodes = []string{"127.0.0.1:6380"}
	if err := source.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(source.RedisAddrs()) != 2 {
		t.Fatalf("addrs: %#v", source.RedisAddrs())
	}

	sentinel := Source{ID: "r2", Name: "ha", Kind: KindRedis, Host: "10.0.0.1", Port: 26379, RedisMode: "sentinel", MaxResultBytes: 1 << 20}
	sentinel.Defaults()
	if err := sentinel.Validate(); err == nil {
		t.Fatal("sentinel without master name should fail")
	}
	sentinel.RedisMasterName = "mymaster"
	if err := sentinel.Validate(); err != nil {
		t.Fatal(err)
	}
	if sentinel.RedisTopology() != "sentinel" {
		t.Fatalf("topology %s", sentinel.RedisTopology())
	}
}
