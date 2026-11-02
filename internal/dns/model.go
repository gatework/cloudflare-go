// Package dns owns record selection and mutations, independently of the SDK.
package dns

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
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
	Tags    []string `json:"tags,omitempty"`
	Comment *string  `json:"comment,omitempty"`
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
	if q.Proxied != nil && r.Proxied != *q.Proxied || q.Content != nil && r.Content != *q.Content {
		return false
	}
	if len(q.Tags) > 0 || q.Comment != nil {
		var fields struct {
			Tags    []string `json:"tags"`
			Comment *string  `json:"comment"`
		}
		if json.Unmarshal(r.Raw, &fields) != nil {
			return false
		}
		if q.Comment != nil && (fields.Comment == nil || *fields.Comment != *q.Comment) {
			return false
		}
		for _, tag := range q.Tags {
			if !slices.Contains(fields.Tags, tag) {
				return false
			}
		}
	}
	return true
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
	ListRecords(context.Context, string, Query) ([]Record, error)
	GetRecord(context.Context, string, string) (Record, error)
	CreateRecord(context.Context, string, RecordPatch) (Record, error)
	UpdateRecord(context.Context, string, string, RecordPatch) (Record, error)
	DeleteRecord(context.Context, string, string) error
	ExportZone(context.Context, string) (string, error)
	CheckDNSRead(context.Context, string) error
}
type Service struct {
	API       API
	AccountID string
}
