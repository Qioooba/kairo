package dbconsole

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	redis "github.com/redis/go-redis/v9"
)

type RedisScanResult struct {
	Keys       []RedisKeyRef `json:"keys"`
	NextCursor string        `json:"next_cursor"`
	Topology   string        `json:"topology,omitempty"`
	Masters    int           `json:"masters,omitempty"`
}

// RedisKeyRef keeps the transport identifier binary-safe. Redis keys are byte
// strings and must never be round-tripped through JSON as assumed UTF-8 text.
type RedisKeyRef struct {
	Display   any    `json:"display"`
	KeyBase64 string `json:"key_base64"`
	Bytes     int    `json:"bytes"`
}

type RedisKey struct {
	Key       any    `json:"key"`
	Type      string `json:"type"`
	TTLMillis int64  `json:"ttl_ms"`
	Value     any    `json:"value"`
	Truncated bool   `json:"truncated"`
}

func (m *Manager) RedisScan(ctx context.Context, source Source, cursor string, pattern string, count int64) (RedisScanResult, error) {
	if source.Kind != KindRedis {
		return RedisScanResult{}, errors.New("数据源不是 Redis")
	}
	if count <= 0 || count > 500 {
		count = 200
	}
	if pattern == "" {
		pattern = "*"
	}
	if len(pattern) > 512 {
		return RedisScanResult{}, errors.New("pattern 不能超过 512 字节")
	}
	ctx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(ctx); err != nil {
		return RedisScanResult{}, err
	}
	defer m.release()
	client, err := m.redisClient(source)
	if err != nil {
		return RedisScanResult{}, err
	}
	keys, next, masters, err := scanRedisKeys(ctx, client, cursor, pattern, count)
	if err != nil {
		return RedisScanResult{}, err
	}
	refs := make([]RedisKeyRef, 0, len(keys))
	for _, key := range keys {
		raw := []byte(key)
		refs = append(refs, RedisKeyRef{
			Display:   normalizeBytes(raw),
			KeyBase64: base64.StdEncoding.EncodeToString(raw),
			Bytes:     len(raw),
		})
	}
	return RedisScanResult{
		Keys:       refs,
		NextCursor: next,
		Topology:   source.RedisTopology(),
		Masters:    masters,
	}, nil
}

func scanRedisKeys(ctx context.Context, client redis.UniversalClient, cursor, pattern string, count int64) ([]string, string, int, error) {
	if cluster, ok := client.(*redis.ClusterClient); ok {
		keys, next, masters, err := scanClusterKeys(ctx, cluster, cursor, pattern, count)
		return keys, next, masters, err
	}
	pos, _ := strconv.ParseUint(strings.TrimSpace(cursor), 10, 64)
	keys, next, err := client.Scan(ctx, pos, pattern, count).Result()
	return keys, strconv.FormatUint(next, 10), 1, err
}

func parseClusterCursor(raw string) (node int, cursor uint64) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		return 0, 0
	}
	nodeStr, curStr, ok := strings.Cut(raw, ":")
	if !ok {
		cursor, _ = strconv.ParseUint(raw, 10, 64)
		return 0, cursor
	}
	node, _ = strconv.Atoi(nodeStr)
	cursor, _ = strconv.ParseUint(curStr, 10, 64)
	if node < 0 {
		return 0, 0
	}
	return node, cursor
}

func formatClusterCursor(node int, cursor uint64, done bool) string {
	if done {
		return "0"
	}
	return strconv.Itoa(node) + ":" + strconv.FormatUint(cursor, 10)
}

func clusterMasterCount(ctx context.Context, cluster *redis.ClusterClient) (int, error) {
	masters, err := snapshotClusterMasters(ctx, cluster)
	if err != nil {
		return 0, err
	}
	return len(masters), nil
}

func snapshotClusterMasters(ctx context.Context, cluster *redis.ClusterClient) ([]*redis.Client, error) {
	var mu sync.Mutex
	byAddr := map[string]*redis.Client{}
	err := cluster.ForEachMaster(ctx, func(ctx context.Context, master *redis.Client) error {
		addr := master.Options().Addr
		mu.Lock()
		byAddr[addr] = master
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}
	addrs := make([]string, 0, len(byAddr))
	for addr := range byAddr {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	out := make([]*redis.Client, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, byAddr[addr])
	}
	return out, nil
}

func scanClusterKeys(ctx context.Context, cluster *redis.ClusterClient, cursor, pattern string, count int64) ([]string, string, int, error) {
	masters, err := snapshotClusterMasters(ctx, cluster)
	if err != nil {
		return nil, "0", 0, err
	}
	if len(masters) == 0 {
		return nil, "0", 0, errors.New("Cluster 没有可用的主节点")
	}
	node, inner := parseClusterCursor(cursor)
	if node >= len(masters) {
		return nil, "0", len(masters), nil
	}
	var keys []string
	for node < len(masters) && int64(len(keys)) < count {
		batch, next, err := masters[node].Scan(ctx, inner, pattern, count-int64(len(keys))).Result()
		if err != nil {
			return nil, "0", len(masters), err
		}
		keys = append(keys, batch...)
		if next == 0 {
			node++
			inner = 0
			continue
		}
		return keys, formatClusterCursor(node, next, false), len(masters), nil
	}
	return keys, formatClusterCursor(node, inner, node >= len(masters)), len(masters), nil
}

func (m *Manager) RedisGet(ctx context.Context, source Source, key string) (RedisKey, error) {
	if source.Kind != KindRedis {
		return RedisKey{}, errors.New("数据源不是 Redis")
	}
	if key == "" {
		return RedisKey{}, errors.New("key 不能为空")
	}
	if len(key) > 4096 {
		return RedisKey{}, errors.New("V1 只支持查看不超过 4096 字节的 key")
	}
	ctx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(ctx); err != nil {
		return RedisKey{}, err
	}
	defer m.release()
	client, err := m.redisClient(source)
	if err != nil {
		return RedisKey{}, err
	}
	pipe := client.Pipeline()
	typeCmd := pipe.Type(ctx, key)
	ttlCmd := pipe.PTTL(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil {
		return RedisKey{}, err
	}
	typ := typeCmd.Val()
	if typ == "none" {
		return RedisKey{}, fmt.Errorf("key 不存在或已过期")
	}
	result := RedisKey{Key: normalizeBytes([]byte(key)), Type: typ, TTLMillis: ttlCmd.Val().Milliseconds()}
	const limit = int64(200)
	budget := source.MaxResultBytes
	if budget > 8<<20 {
		budget = 8 << 20
	}
	var used int64
	switch typ {
	case "string":
		// GET 会先把完整 value 分配到进程内存，单个异常大 key 就能绕过
		// MaxResultBytes。GETRANGE 只取 normalizeBytes 能展示的最大文本再多
		// 4 字节（覆盖 UTF-8 最长 rune），既能安全截断，也不会把半个中文
		// 字符误判成二进制。
		pipe := client.Pipeline()
		valueCmd := pipe.GetRange(ctx, key, 0, maxCellBytes+3)
		lengthCmd := pipe.StrLen(ctx, key)
		if _, err := pipe.Exec(ctx); err != nil {
			return result, err
		}
		value, err := valueCmd.Result()
		if err != nil {
			return result, err
		}
		result.Value = normalizeBytes([]byte(value))
		result.Truncated = lengthCmd.Val() > maxCellBytes
	case "hash":
		items, next, err := client.HScan(ctx, key, 0, "*", limit).Result()
		if err != nil {
			return result, err
		}
		pairs := make([]any, 0, len(items)/2)
		for i := 0; i+1 < len(items); i += 2 {
			item := map[string]any{"field": normalizeBytes([]byte(items[i])), "value": normalizeBytes([]byte(items[i+1]))}
			if !appendRedisPreview(&pairs, item, &used, budget) {
				result.Truncated = true
				break
			}
		}
		result.Value, result.Truncated = pairs, result.Truncated || next != 0
	case "list":
		items, err := client.LRange(ctx, key, 0, limit-1).Result()
		if err != nil {
			return result, err
		}
		length, _ := client.LLen(ctx, key).Result()
		result.Value, result.Truncated = boundedRedisStrings(items, budget)
		result.Truncated = result.Truncated || length > int64(len(items))
	case "set":
		items, next, err := client.SScan(ctx, key, 0, "*", limit).Result()
		if err != nil {
			return result, err
		}
		result.Value, result.Truncated = boundedRedisStrings(items, budget)
		result.Truncated = result.Truncated || next != 0
	case "zset":
		items, err := client.ZRangeWithScores(ctx, key, 0, limit-1).Result()
		if err != nil {
			return result, err
		}
		length, _ := client.ZCard(ctx, key).Result()
		preview := make([]any, 0, len(items))
		for _, item := range items {
			value := map[string]any{"member": normalizeValue(item.Member), "score": item.Score}
			if !appendRedisPreview(&preview, value, &used, budget) {
				result.Truncated = true
				break
			}
		}
		result.Value, result.Truncated = preview, result.Truncated || length > int64(len(items))
	case "stream":
		items, err := client.XRangeN(ctx, key, "-", "+", limit).Result()
		if err != nil {
			return result, err
		}
		length, _ := client.XLen(ctx, key).Result()
		preview := make([]any, 0, len(items))
		for _, item := range items {
			values := make([]any, 0, len(item.Values))
			for field, value := range item.Values {
				values = append(values, map[string]any{
					"field": normalizeBytes([]byte(field)),
					"value": normalizeValue(value),
				})
			}
			if !appendRedisPreview(&preview, map[string]any{"id": item.ID, "values": values}, &used, budget) {
				result.Truncated = true
				break
			}
		}
		result.Value, result.Truncated = preview, result.Truncated || length > int64(len(items))
	default:
		result.Value = map[string]any{"message": "一期暂不展开该 Redis 类型", "read_at": time.Now().Format(time.RFC3339)}
	}
	return result, nil
}

func boundedRedisStrings(items []string, budget int64) ([]any, bool) {
	out := make([]any, 0, len(items))
	var used int64
	for _, item := range items {
		if !appendRedisPreview(&out, normalizeBytes([]byte(item)), &used, budget) {
			return out, true
		}
	}
	return out, false
}

func appendRedisPreview(out *[]any, value any, used *int64, budget int64) bool {
	raw, err := json.Marshal(value)
	if err != nil || *used+int64(len(raw)) > budget {
		return false
	}
	*used += int64(len(raw))
	*out = append(*out, value)
	return true
}
