// Package dns owns record selection and mutations, independently of the SDK.
package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalid   = errors.New("记录参数无效")
	ErrNotFound  = errors.New("未找到匹配记录或 Zone")
	ErrConflict  = errors.New("存在歧义或记录已变化")
	ErrUncertain = errors.New("写入结果不确定，请重新查询后判断；未自动重试")
)

type Zone struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AccountID string `json:"account_id,omitempty"`
	Status    string `json:"status,omitempty"`
}
type Record struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Name     string          `json:"name"`
	Content  string          `json:"content"`
	TTL      int             `json:"ttl"`
	Proxied  bool            `json:"proxied"`
	Modified string          `json:"modified_on"`
	Raw      json.RawMessage `json:"-"`
}

func ParseRecord(raw []byte) (Record, error) {
	var record Record
	if json.Unmarshal(raw, &record) != nil || record.ID == "" || record.Name == "" || record.Type == "" {
		return record, errors.New("API 返回无效 DNS 记录")
	}
	record.Raw = append(json.RawMessage(nil), raw...)
	return record, nil
}
func (r Record) MarshalJSON() ([]byte, error) {
	if len(r.Raw) == 0 {
		return nil, errors.New("记录缺少原始数据")
	}
	return r.Raw, nil
}

type Query struct {
	Domain  string   `json:"domain"`
	Exact   bool     `json:"exact"`
	Types   []string `json:"types,omitempty"`
	Proxied *bool    `json:"proxied,omitempty"`
	Content *string  `json:"content,omitempty"`
}

func (q Query) Match(r Record) bool {
	name := strings.ToLower(strings.TrimSuffix(r.Name, "."))
	if q.Domain != "" && (name != q.Domain && (q.Exact || !strings.HasSuffix(name, "."+q.Domain))) {
		return false
	}
	if len(q.Types) > 0 {
		found := false
		for _, typ := range q.Types {
			if r.Type == typ {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return (q.Proxied == nil || r.Proxied == *q.Proxied) && (q.Content == nil || r.Content == *q.Content)
}

type Selector struct {
	Query
	ID string
}
type Dump struct {
	SchemaVersion int       `json:"schema_version"`
	ExportedAt    time.Time `json:"exported_at"`
	Zone          Zone      `json:"zone"`
	Query         Query     `json:"query"`
	Records       []Record  `json:"records"`
}
type Change struct {
	Action   string                     `json:"action"`
	Zone     Zone                       `json:"zone"`
	RecordID string                     `json:"record_id,omitempty"`
	Before   *Record                    `json:"before,omitempty"`
	Changes  map[string]json.RawMessage `json:"changes,omitempty"`
}
type RecordPatch map[string]json.RawMessage

type API interface {
	ListZones(context.Context, string, string) ([]Zone, error)
	GetZone(context.Context, string) (Zone, error)
	ListRecords(context.Context, string, Query, int64) ([]Record, error)
	GetRecord(context.Context, string, string) (Record, error)
	CreateRecord(context.Context, string, RecordPatch) (Record, error)
	UpdateRecord(context.Context, string, string, RecordPatch) (Record, error)
	DeleteRecord(context.Context, string, string) error
	ExportZone(context.Context, string) (string, error)
}
type Service struct {
	API       API
	AccountID string
	MaxBytes  int64
}

func (s *Service) Zone(ctx context.Context, domain, zoneName, zoneID string) (Zone, error) {
	var zone Zone
	if zoneID != "" {
		var err error
		zone, err = s.API.GetZone(ctx, zoneID)
		if err != nil {
			return zone, err
		}
	} else {
		name := domain
		if zoneName != "" {
			name = zoneName
		}
		for name != "" {
			// Service labels and wildcard owners are legal record names, but
			// cannot be Zone names. Skip them while walking toward the apex.
			if _, err := Name(name, "", false); err != nil {
				if zoneName != "" {
					return zone, err
				}
				_, name, _ = strings.Cut(name, ".")
				continue
			}
			zones, err := s.API.ListZones(ctx, name, s.AccountID)
			if err != nil {
				return zone, err
			}
			if len(zones) > 1 {
				return zone, fmt.Errorf("%w：请指定 --zone-id 或 account_id", ErrConflict)
			}
			if len(zones) == 1 {
				zone = zones[0]
				break
			}
			if zoneName != "" {
				break
			}
			_, name, _ = strings.Cut(name, ".")
		}
		if zone.ID == "" {
			return zone, ErrNotFound
		}
	}
	if s.AccountID != "" && zone.AccountID != s.AccountID {
		return Zone{}, fmt.Errorf("%w：Zone 不属于所选账号", ErrConflict)
	}
	if zoneName != "" && zone.Name != zoneName {
		return Zone{}, fmt.Errorf("%w：Zone 名称与 ID 不一致", ErrConflict)
	}
	if domain != "" && !Within(domain, zone.Name) {
		return Zone{}, errors.New("记录名称不属于所选 Zone")
	}
	return zone, nil
}
func (s *Service) Select(ctx context.Context, z Zone, selector Selector) (Record, error) {
	if selector.ID != "" {
		record, err := s.API.GetRecord(ctx, z.ID, selector.ID)
		if err != nil {
			return record, err
		}
		if !selector.Query.Match(record) {
			return Record{}, fmt.Errorf("%w：ID 与名称或筛选条件不一致", ErrConflict)
		}
		return record, nil
	}
	selector.Exact = true
	records, err := s.API.ListRecords(ctx, z.ID, selector.Query, s.MaxBytes)
	if err != nil {
		return Record{}, err
	}
	if len(records) == 0 {
		return Record{}, ErrNotFound
	}
	if len(records) > 1 {
		ids := make([]string, len(records))
		for i, r := range records {
			ids[i] = r.ID
		}
		return Record{}, fmt.Errorf("%w：候选 ID %s；请使用 --id", ErrConflict, strings.Join(ids, ", "))
	}
	return records[0], nil
}
func (s *Service) Prepare(ctx context.Context, z Zone, action string, selector Selector, patch RecordPatch) (Change, error) {
	change := Change{Action: action, Zone: z, Changes: patch}
	if action != "create" {
		before, err := s.Select(ctx, z, selector)
		if err != nil {
			return change, err
		}
		change.Before = &before
		change.RecordID = before.ID
		if action == "update" {
			// Preserve nested record data/settings when the API replaces the
			// supplied object rather than merging its members.
			var previous map[string]json.RawMessage
			_ = json.Unmarshal(before.Raw, &previous)
			for _, key := range []string{"data", "settings"} {
				if incoming, ok := patch[key]; ok {
					var merged map[string]json.RawMessage
					_ = json.Unmarshal(previous[key], &merged)
					if merged == nil {
						merged = map[string]json.RawMessage{}
					}
					var fields map[string]json.RawMessage
					if json.Unmarshal(incoming, &fields) != nil || fields == nil {
						return change, errors.New("data/settings 需要对象")
					}
					for k, v := range fields {
						merged[k] = v
					}
					encoded, _ := json.Marshal(merged)
					patch[key] = encoded
				}
			}
			if typ, ok := patch["type"]; ok {
				var value string
				if json.Unmarshal(typ, &value) != nil || !strings.EqualFold(value, before.Type) {
					return change, fmt.Errorf("%w：首版不支持原地转换记录类型", ErrInvalid)
				}
				patch["type"] = json.RawMessage(quote(before.Type))
			}
			if before.Type == "MX" {
				var fields map[string]json.RawMessage
				if json.Unmarshal(patch["data"], &fields) == nil && len(fields) == 1 && fields["priority"] != nil {
					patch["priority"] = fields["priority"]
					delete(patch, "data")
				}
			}
		}
	}
	if action != "delete" {
		name := selector.Domain
		typ := ""
		if change.Before != nil {
			name = change.Before.Name
			typ = change.Before.Type
		}
		if action == "create" {
			patch["name"] = json.RawMessage(quote(name))
		}
		if err := ValidatePatch(patch, action == "create", z.Name, typ); err != nil {
			return change, fmt.Errorf("%w：%w", ErrInvalid, err)
		}
		if action == "create" {
			var q Query
			q.Domain = name
			q.Exact = true
			_ = json.Unmarshal(patch["type"], &typ)
			q.Types = []string{typ}
			existing, err := s.API.ListRecords(ctx, z.ID, q, s.MaxBytes)
			if err != nil {
				return change, err
			}
			for _, r := range existing {
				if MatchesPatch(r, patch) {
					return change, fmt.Errorf("%w：相同记录已存在（ID %s）", ErrConflict, r.ID)
				}
			}
		}
	}
	return change, nil
}
func (s *Service) Apply(ctx context.Context, change Change) (any, error) {
	if change.Before != nil {
		latest, err := s.API.GetRecord(ctx, change.Zone.ID, change.RecordID)
		if err != nil {
			return nil, err
		}
		if !EqualJSON(latest.Raw, change.Before.Raw) {
			return nil, fmt.Errorf("%w：目标记录在准备后发生变化", ErrConflict)
		}
	}
	var record Record
	var err error
	switch change.Action {
	case "create":
		record, err = s.API.CreateRecord(ctx, change.Zone.ID, change.Changes)
	case "update":
		record, err = s.API.UpdateRecord(ctx, change.Zone.ID, change.RecordID, change.Changes)
	case "delete":
		err = s.API.DeleteRecord(ctx, change.Zone.ID, change.RecordID)
	default:
		return nil, errors.New("未知 DNS 操作")
	}
	if err != nil {
		var definitive interface{ Definitive() bool }
		if errors.As(err, &definitive) && definitive.Definitive() {
			return nil, err
		}
		// Read-only verification never repeats the mutation. Creation has no reliable
		// server-issued ID after a lost response, so cannot safely claim success.
		if change.Action != "create" {
			latest, readErr := s.API.GetRecord(ctx, change.Zone.ID, change.RecordID)
			if change.Action == "delete" && errors.Is(readErr, ErrNotFound) {
				return map[string]any{"id": change.RecordID, "deleted": true, "verified_by_read": true}, nil
			}
			if change.Action == "update" && readErr == nil && MatchesPatch(latest, change.Changes) {
				return latest, nil
			}
		}
		return nil, ErrUncertain
	}
	if change.Action == "delete" {
		return map[string]any{"id": change.RecordID, "deleted": true}, nil
	}
	return record, nil
}
func quote(s string) string { b, _ := json.Marshal(s); return string(b) }
func EqualJSON(a, b []byte) bool {
	var x, y any
	da, db := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	da.UseNumber()
	db.UseNumber()
	if da.Decode(&x) != nil || db.Decode(&y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}
func MatchesPatch(r Record, patch RecordPatch) bool {
	var data map[string]json.RawMessage
	_ = json.Unmarshal(r.Raw, &data)
	for k, v := range patch {
		if !EqualJSON(v, data[k]) {
			return false
		}
	}
	return true
}
