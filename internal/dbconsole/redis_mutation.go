package dbconsole

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RedisMutateTTL is the deliberately narrow first write capability. It never
// deletes values and requires the source-level capability plus an admin-side
// confirmation in the HTTP layer.
func (m *Manager) RedisMutateTTL(ctx context.Context, source Source, key, operation string, seconds int64) (any, error) {
	if source.Kind != KindRedis { return nil, errors.New("数据源不是 Redis") }
	if !source.AllowRedisWrite { return nil, errors.New("该 Redis 数据源未开启受控写操作") }
	if strings.TrimSpace(key) == "" || len(key) > 4096 { return nil, errors.New("key 不能为空且不能超过 4096 字节") }
	operation = strings.ToUpper(strings.TrimSpace(operation))
	if operation != "EXPIRE" && operation != "PEXPIRE" && operation != "PERSIST" { return nil, fmt.Errorf("不支持的 Redis TTL 操作 %s", operation) }
	if operation != "PERSIST" && (seconds <= 0 || seconds > 365*24*60*60) { return nil, errors.New("TTL 必须在 1 秒到 365 天之间") }
	ctx, cancel := context.WithTimeout(ctx, source.Timeout()); defer cancel()
	if err := m.acquire(ctx); err != nil { return nil, err }; defer m.release()
	client, err := m.redisClient(source); if err != nil { return nil, err }
	var result any
	if operation == "PERSIST" { result, err = client.Do(ctx, operation, key).Result() } else if operation == "PEXPIRE" { result, err = client.Do(ctx, operation, key, seconds*1000).Result() } else { result, err = client.Do(ctx, operation, key, seconds).Result() }
	if err != nil { return nil, err }
	if value, ok := result.(int64); ok { return map[string]any{"operation": operation, "key": key, "seconds": seconds, "changed": value == 1, "result": value, "at": time.Now().Format(time.RFC3339), "argument": strconv.FormatInt(seconds, 10)}, nil }
	return result, nil
}
