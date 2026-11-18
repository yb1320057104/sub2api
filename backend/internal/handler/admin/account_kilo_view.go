package admin

import (
	"context"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// listKiloAccountsByCurrentConcurrency obtains the logical candidate set in one
// ListAllWithFilters read, before taking the shared ordering/display snapshot.
func (h *AccountHandler) listKiloAccountsByCurrentConcurrency(ctx context.Context, page, pageSize int, platform, accountType, status, search string, groupID int64, privacyMode, sortOrder string) ([]service.Account, int64, map[int64]int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	accounts, err := h.adminService.ListAccountsForSchedulerScoreFilter(ctx, platform, accountType, status, search, groupID, privacyMode)
	if err != nil {
		return nil, 0, nil, err
	}
	ids := make([]int64, len(accounts))
	for i := range accounts {
		ids[i] = accounts[i].ID
	}
	counts, err := h.concurrencyService.GetAccountConcurrencySnapshot(ctx, ids)
	if err != nil {
		return nil, 0, nil, infraerrors.ServiceUnavailable("ACCOUNT_CONCURRENCY_UNAVAILABLE", "Current account concurrency is unavailable; retry or choose another sort order").WithCause(err)
	}
	desc := pagination.NormalizeSortOrder(sortOrder, pagination.SortOrderAsc) == pagination.SortOrderDesc
	sort.Slice(accounts, func(i, j int) bool {
		left, right := counts[accounts[i].ID], counts[accounts[j].ID]
		if left == right {
			if desc {
				return accounts[i].ID > accounts[j].ID
			}
			return accounts[i].ID < accounts[j].ID
		}
		if desc {
			return left > right
		}
		return left < right
	})
	total := int64(len(accounts))
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	start := params.Offset()
	if start >= len(accounts) {
		return []service.Account{}, total, counts, nil
	}
	return accounts[start:min(start+params.Limit(), len(accounts))], total, counts, nil
}

// enrichKiloAccountViews batches only the current response's managed Key IDs.
// A source summary is admin-only because it can belong to another group.
func (h *AccountHandler) enrichKiloAccountViews(ctx context.Context, items []AccountWithConcurrency) error {
	if _, observer := service.ObserverGroupIDs(ctx); observer {
		for i := range items {
			dto.AccountForObserver(ctx, items[i].Account)
		}
		return nil
	}
	reader, ok := h.adminService.(service.KiloAccountViewReader)
	if !ok {
		return nil
	}
	seen := make(map[int64]struct{})
	ids := make([]int64, 0, len(items))
	for i := range items {
		a := items[i].Account
		if a == nil || a.Platform != service.PlatformOpenAI || a.Type != service.AccountTypeAPIKey || a.Extra[service.KiloManagedExtraKey] != true {
			continue
		}
		if _, exists := seen[a.ID]; !exists {
			seen[a.ID] = struct{}{}
			ids = append(ids, a.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	views, err := reader.GetKiloAccountViews(ctx, ids)
	if err != nil {
		return err
	}
	for i := range items {
		if a := items[i].Account; a != nil {
			if view, exists := views[a.ID]; exists {
				a.Kilo = &view
			}
		}
	}
	return nil
}
