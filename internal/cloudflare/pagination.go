package cloudflare

import (
	"errors"
	"fmt"

	"github.com/gatework/cloudflare-go/internal/dns"
)

// Pointers distinguish an omitted statistic from an explicitly reported zero.
type pageInfo struct {
	Page       *int `json:"page"`
	Count      *int `json:"count"`
	TotalPages *int `json:"total_pages"`
	TotalCount *int `json:"total_count"`
}

type pageTracker struct{ totalPages, totalCount *int }

func (p *pageTracker) observe(info pageInfo, page, count int) (bool, error) {
	if info.Page != nil && *info.Page != page || info.Count != nil && *info.Count != count {
		return false, errors.New("API 分页编号或条数不一致")
	}
	for _, pair := range []struct {
		previous **int
		current  *int
	}{{&p.totalPages, info.TotalPages}, {&p.totalCount, info.TotalCount}} {
		if pair.current == nil {
			continue
		}
		if *pair.current < 0 {
			return false, errors.New("API 分页统计不能为负数")
		}
		if *pair.previous != nil && **pair.previous != *pair.current {
			return false, fmt.Errorf("%w：分页期间结果统计发生变化", dns.ErrConflict)
		}
		*pair.previous = pair.current
	}
	if p.totalCount != nil && *p.totalCount == 0 && count != 0 {
		return false, errors.New("API 零条统计与记录不一致")
	}
	if p.totalPages != nil {
		total := *p.totalPages
		if total == 0 {
			if page != 1 || count != 0 {
				return false, errors.New("API 零页统计与记录不一致")
			}
			return true, nil
		}
		if page > total || count == 0 && page < total {
			return false, errors.New("API 分页提前结束，结果不完整")
		}
		return page == total || count == 0, nil
	}
	return count == 0, nil
}

func (p *pageTracker) complete(unique int) error {
	if p.totalCount != nil && unique != *p.totalCount {
		return errors.New("API 分页记录总数不符，结果不完整")
	}
	return nil
}
