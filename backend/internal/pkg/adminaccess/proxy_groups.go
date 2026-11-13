package adminaccess

import "slices"

// AllowsProxyGroupAssignment requires an explicit global grant because the
// selected proxies' capacity is shared with accounts outside this key's scope.
// Each account operation still uses the existing indivisible resource rules.
func (p Policy) AllowsProxyGroupAssignment(a Account, changesConcurrency bool) bool {
	return slices.Contains(p.GlobalPermissions, "proxy-groups.assign") &&
		p.AllowsAccount("accounts.read", a) &&
		p.AllowsAccount("accounts.network", a) &&
		(!changesConcurrency || p.AllowsAccount("accounts.schedule", a))
}
