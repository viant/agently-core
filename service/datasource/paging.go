package datasource

import (
	"math"
	"strings"

	"github.com/viant/forge/backend/types"
)

func applyPaging(rows []map[string]interface{}, dataInfo map[string]interface{}, ds *types.DataSource, args map[string]interface{}) ([]map[string]interface{}, map[string]interface{}) {
	if ds == nil || ds.Paging == nil || !ds.Paging.Enabled {
		return rows, dataInfo
	}

	pageName := "page"
	sizeName := "size"
	if ds.Paging.Parameters != nil {
		if ds.Paging.Parameters.Page != "" {
			pageName = ds.Paging.Parameters.Page
		}
		if ds.Paging.Parameters.Size != "" {
			sizeName = ds.Paging.Parameters.Size
		}
	}

	page := intArg(args, pageName, 1)
	if page < 1 {
		page = 1
	}
	pageSize := intArg(args, sizeName, ds.Paging.Size)
	if pageSize <= 0 {
		pageSize = ds.Paging.Size
	}
	if pageSize <= 0 {
		pageSize = len(rows)
	}
	if pageSize <= 0 {
		pageSize = 1
	}

	if dataInfo == nil {
		dataInfo = map[string]interface{}{}
	}
	totalCount := dataInfoTotalCount(dataInfo, ds, len(rows))
	if totalCount <= 0 {
		totalCount = len(rows)
	}
	pageCount := int(math.Ceil(float64(totalCount) / float64(pageSize)))
	if pageCount <= 0 {
		pageCount = 1
	}

	if strings.EqualFold(pageName, "offset") {
		offset := intArg(args, pageName, 0)
		if offset < 0 {
			offset = 0
		}
		page = 1
		if pageSize > 0 {
			page = (offset / pageSize) + 1
		}
	} else {
		start := (page - 1) * pageSize
		if start < 0 {
			start = 0
		}
		if totalCount == len(rows) {
			if start >= len(rows) {
				rows = []map[string]interface{}{}
			} else {
				end := start + pageSize
				if end > len(rows) {
					end = len(rows)
				}
				rows = rows[start:end]
			}
		}
	}

	dataInfo["pageCount"] = pageCount
	dataInfo["totalCount"] = totalCount
	dataInfo["page"] = page
	dataInfo["pageSize"] = pageSize
	return rows, dataInfo
}

func dataInfoTotalCount(dataInfo map[string]interface{}, ds *types.DataSource, fallback int) int {
	if dataInfo == nil {
		return fallback
	}
	if ds != nil && ds.Paging != nil && ds.Paging.DataInfoSelectors != nil {
		if key := strings.TrimSpace(ds.Paging.DataInfoSelectors.TotalCount); key != "" {
			if total := intArg(dataInfo, key, 0); total > 0 {
				return total
			}
		}
	}
	if total := intArg(dataInfo, "totalCount", 0); total > 0 {
		return total
	}
	if total := intArg(dataInfo, "recordCount", 0); total > 0 {
		return total
	}
	return fallback
}

func intArg(holder map[string]interface{}, key string, fallback int) int {
	if holder == nil {
		return fallback
	}
	value, ok := holder[key]
	if !ok {
		return fallback
	}
	switch actual := value.(type) {
	case int:
		return actual
	case int32:
		return int(actual)
	case int64:
		return int(actual)
	case float64:
		return int(actual)
	case float32:
		return int(actual)
	default:
		return fallback
	}
}
