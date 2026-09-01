package dbconsole

import "testing"

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
