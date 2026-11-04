package service

import (
	"strconv"
	"strings"
)

// The versioned lease carries its original physical reservation set. Persistent
// Live controllers can refresh/release it after a restart or a binding edit
// without reconstructing that set from mutable database configuration.
func NewAccountProxyLeaseID(accountID int64, resources []int64, strict bool, nonce string) string {
	parts := make([]string, 0, len(resources))
	for _, id := range uniqueProxyResourceIDs(resources) {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	mode := "0"
	if strict {
		mode = "1"
	}
	return "p2:" + strconv.FormatInt(accountID, 10) + ":" + mode + ":" + strings.Join(parts, ",") + ":" + nonce
}

func ParseAccountProxyLeaseID(lease string) (int64, []int64, bool, bool) {
	parts := strings.Split(lease, ":")
	if len(parts) != 5 || parts[0] != "p2" || (parts[2] != "0" && parts[2] != "1") || parts[4] == "" {
		return 0, nil, false, false
	}
	accountID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || accountID <= 0 {
		return 0, nil, false, false
	}
	var resources []int64
	for _, part := range strings.Split(parts[3], ",") {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			return 0, nil, false, false
		}
		resources = append(resources, id)
	}
	if len(resources) == 0 {
		return 0, nil, false, false
	}
	return accountID, uniqueProxyResourceIDs(resources), parts[2] == "1", true
}
