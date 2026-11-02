package dns

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

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
	records, err := s.API.ListRecords(ctx, z.ID, selector.Query)
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
