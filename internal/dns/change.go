package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
)

func (s *Service) Prepare(ctx context.Context, z Zone, action string, selector Selector, patch RecordPatch) (Change, error) {
	if action != "create" && action != "update" && action != "delete" {
		return Change{}, fmt.Errorf("%w：未知 DNS 操作", ErrInvalid)
	}
	// Validation normalizes fields. A prepared change owns its snapshot; neither
	// normalization nor later caller edits may change the original intent.
	patch = maps.Clone(patch)
	if patch == nil {
		patch = RecordPatch{}
	}
	for key, value := range patch {
		patch[key] = bytes.Clone(value)
	}
	change := Change{Action: action, Zone: z, Changes: patch}
	if action != "create" {
		before, err := s.Select(ctx, z, selector)
		if err != nil {
			return change, err
		}
		change.Before = &before
		change.RecordID = before.ID
		if action == "update" {
			if typ, ok := patch["type"]; ok {
				var value string
				if json.Unmarshal(typ, &value) != nil || !strings.EqualFold(value, before.Type) {
					return change, fmt.Errorf("%w：不支持原地转换记录类型", ErrInvalid)
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
		if action == "update" {
			if err := mergeRecordObjects(patch, change.Before.Raw); err != nil {
				return change, err
			}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

// Validate user input before merging, so future fields returned by the API are
// preserved without becoming writable CLI aliases or being rejected on update.
func mergeRecordObjects(patch RecordPatch, before json.RawMessage) error {
	var previous map[string]json.RawMessage
	if err := json.Unmarshal(before, &previous); err != nil {
		return err
	}
	for _, key := range []string{"data", "settings"} {
		incoming, ok := patch[key]
		if !ok {
			continue
		}
		merged := map[string]json.RawMessage{}
		if old := previous[key]; len(old) > 0 && !bytes.Equal(bytes.TrimSpace(old), []byte("null")) {
			if err := json.Unmarshal(old, &merged); err != nil {
				return errors.New("API 返回的 data/settings 不是对象")
			}
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(incoming, &fields); err != nil {
			return err
		}
		for k, v := range fields {
			merged[k] = v
		}
		encoded, err := json.Marshal(merged)
		if err != nil {
			return err
		}
		patch[key] = encoded
	}
	return nil
}
