package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type Store struct {
	RDB *redis.Client
}

func Connect(ctx context.Context, url string) (*Store, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return &Store{RDB: rdb}, nil
}

func (s *Store) Close() error { return s.RDB.Close() }

func deviceKey(id string) string  { return "state:device:" + id }
func linkKey(id string) string    { return "state:link:" + id }
func oidKey(id string) string     { return "snmp:values:" + id }
func colKey(id string) string     { return "snmp:columns:" + id }
func counterKey(id string) string { return "snmp:counters:" + id }

func (s *Store) SetDevice(ctx context.Context, deviceID string, fields map[string]any) {
	if len(fields) == 0 {
		return
	}
	values := make(map[string]any, len(fields))
	for k, v := range fields {
		values[k] = fmt.Sprint(v)
	}
	values["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	_ = s.RDB.HSet(ctx, deviceKey(deviceID), values).Err()
}

func (s *Store) GetDevice(ctx context.Context, deviceID string) map[string]string {
	m, _ := s.RDB.HGetAll(ctx, deviceKey(deviceID)).Result()
	return m
}

func (s *Store) SetLink(ctx context.Context, linkID string, fields map[string]any) {
	if len(fields) == 0 {
		return
	}
	values := make(map[string]any, len(fields))
	for k, v := range fields {
		values[k] = fmt.Sprint(v)
	}
	values["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	_ = s.RDB.HSet(ctx, linkKey(linkID), values).Err()
}

func (s *Store) GetLink(ctx context.Context, linkID string) map[string]string {
	m, _ := s.RDB.HGetAll(ctx, linkKey(linkID)).Result()
	return m
}

func (s *Store) SetOID(ctx context.Context, deviceID, oid string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	_ = s.RDB.HSet(ctx, oidKey(deviceID), oid, string(data)).Err()
	_ = s.RDB.Expire(ctx, oidKey(deviceID), 10*time.Minute).Err()
}

func (s *Store) GetOIDs(ctx context.Context, deviceID string) map[string]any {
	raw, err := s.RDB.HGetAll(ctx, oidKey(deviceID)).Result()
	if err != nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		var parsed any
		if json.Unmarshal([]byte(v), &parsed) == nil {
			out[k] = parsed
		}
	}
	return out
}

func (s *Store) SetColumn(ctx context.Context, deviceID, baseOID string, values []float64) {
	data, err := json.Marshal(values)
	if err != nil {
		return
	}
	_ = s.RDB.HSet(ctx, colKey(deviceID), baseOID, string(data)).Err()
	_ = s.RDB.Expire(ctx, colKey(deviceID), 10*time.Minute).Err()
}

func (s *Store) SetRate(ctx context.Context, deviceID, oid string, value float64) {
	key := "snmp:rates:" + deviceID
	_ = s.RDB.HSet(ctx, key, oid, fmt.Sprintf("%.6f", value)).Err()
	_ = s.RDB.Expire(ctx, key, 10*time.Minute).Err()
}

func (s *Store) GetRates(ctx context.Context, deviceID string) map[string]float64 {
	raw, err := s.RDB.HGetAll(ctx, "snmp:rates:"+deviceID).Result()
	if err != nil {
		return map[string]float64{}
	}
	out := make(map[string]float64, len(raw))
	for k, v := range raw {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			out[k] = f
		}
	}
	return out
}

func (s *Store) GetColumns(ctx context.Context, deviceID string) map[string][]float64 {
	raw, err := s.RDB.HGetAll(ctx, colKey(deviceID)).Result()
	if err != nil {
		return map[string][]float64{}
	}
	out := make(map[string][]float64, len(raw))
	for k, v := range raw {
		var vals []float64
		if json.Unmarshal([]byte(v), &vals) == nil {
			out[k] = vals
		}
	}
	return out
}

// CounterRate returns (current-previous)/(elapsed seconds) for a SNMP counter.
// A decrease means a counter wrap or a device reboot: the sample is discarded.
// The read/update is a single Lua script so two concurrent probes cannot lose
// each other's previous sample.
var counterRateScript = redis.NewScript(`
local raw = redis.call('HGET', KEYS[1], ARGV[1])
local now = tonumber(ARGV[2])
local current = tonumber(ARGV[3])
local value = string.format('%.0f:%d', current, now)
if not raw then
  redis.call('HSET', KEYS[1], ARGV[1], value)
  redis.call('EXPIRE', KEYS[1], 600)
  return 0
end
local prev, prevMs = string.match(raw, '([^:]+):(%d+)')
if not prev then
  redis.call('HSET', KEYS[1], ARGV[1], value)
  redis.call('EXPIRE', KEYS[1], 600)
  return 0
end
redis.call('HSET', KEYS[1], ARGV[1], value)
redis.call('EXPIRE', KEYS[1], 600)
prev = tonumber(prev)
prevMs = tonumber(prevMs)
if current < prev or now <= prevMs then
  return 0
end
return (current - prev) / ((now - prevMs) / 1000.0)
`)

func (s *Store) CounterRate(ctx context.Context, deviceID, oid string, current float64) float64 {
	key := counterKey(deviceID)
	now := time.Now().UnixMilli()
	value, err := counterRateScript.Run(ctx, s.RDB, []string{key}, oid, now, current).Float64()
	if err != nil {
		return 0
	}
	return value
}

func (s *Store) SetMapSummary(ctx context.Context, mapID string, fields map[string]any) {
	values := map[string]any{}
	for k, v := range fields {
		values[k] = fmt.Sprint(v)
	}
	values["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	_ = s.RDB.HSet(ctx, "state:map:"+mapID+":summary", values).Err()
}

func (s *Store) GetMapSummary(ctx context.Context, mapID string) map[string]string {
	m, _ := s.RDB.HGetAll(ctx, "state:map:"+mapID+":summary").Result()
	return m
}

func (s *Store) IncrRetry(ctx context.Context, serviceID string) int64 {
	n, _ := s.RDB.Incr(ctx, "retry:"+serviceID).Result()
	_ = s.RDB.Expire(ctx, "retry:"+serviceID, 10*time.Minute).Err()
	return n
}

func (s *Store) ResetRetry(ctx context.Context, serviceID string) {
	_ = s.RDB.Del(ctx, "retry:"+serviceID).Err()
}

func (s *Store) PublishAlert(ctx context.Context, payload []byte) {
	_ = s.RDB.Publish(ctx, "pubsub:global_alerts", payload).Err()
}

// RecordFlap appends a transition timestamp and returns how many transitions
// happened in the last 15 minutes.
func (s *Store) RecordFlap(ctx context.Context, deviceID string) int64 {
	key := "flap:" + deviceID
	now := time.Now().UnixMilli()
	pipe := s.RDB.Pipeline()
	pipe.LPush(ctx, key, now)
	pipe.LTrim(ctx, key, 0, 19)
	pipe.Expire(ctx, key, 20*time.Minute)
	pipe.Exec(ctx)
	vals, _ := s.RDB.LRange(ctx, key, 0, -1).Result()
	cutoff := now - 15*60*1000
	var count int64
	for _, raw := range vals {
		var ts int64
		if _, err := fmt.Sscanf(raw, "%d", &ts); err == nil && ts >= cutoff {
			count++
		}
	}
	return count
}

func (s *Store) FlapAlertOnce(ctx context.Context, deviceID string) bool {
	ok, _ := s.RDB.SetNX(ctx, "flap-alert:"+deviceID, "1", 45*time.Minute).Result()
	return ok
}

func (s *Store) LastValueFloat(ctx context.Context, key string) float64 {
	raw, _ := s.RDB.Get(ctx, key).Result()
	f, _ := strconv.ParseFloat(raw, 64)
	return f
}
