package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
	"github.com/stretchr/testify/require"
)

func TestPKBinanceProxyInputNormalizedBeforePersistence(t *testing.T) {
	raw := "http://fixture123-region-HK-sid-Ab12Cd-t-5:fixturePASS123@proxy.invalid:3000"
	input, err := normalizeBinanceRechargeAccountInput(BinanceRechargeAccountInput{Name: "fixture", RecipientUID: "123", ProxyURL: "\uFEFF[" + raw + "](" + raw + ")\u200B"})
	require.NoError(t, err)
	require.Equal(t, raw, input.ProxyURL)
	require.NoError(t, validateBinanceAccountProxy(input.ProxyURL, false))
	// Empty means keep the saved route; invisible-only input cannot erase it.
	_, err = normalizeBinanceRechargeAccountInput(BinanceRechargeAccountInput{Name: "fixture", RecipientUID: "123", ProxyURL: "\u200B"})
	require.ErrorContains(t, err, "没有有效地址")
}

func TestPKBinanceProxyErrorsKeepSafeSpecificReason(t *testing.T) {
	for _, tc := range []struct{ raw, hint string }{
		{"http://fixture:private-secret@proxy.invalid:99999", "端口"},
		{"http://fixture:private-secret@proxy.invalid:80/path", "路径"},
		{"ftp://fixture:private-secret@proxy.invalid:80", "开头"},
		{"http://fixture:private-secret@proxy.\u200Binvalid:80", "隐藏字符"},
	} {
		err := validateBinanceAccountProxy(tc.raw, false)
		require.ErrorIs(t, err, errBinanceAccountProxy)
		var invalid *binancepay.ProxyConfigurationError
		require.True(t, errors.As(err, &invalid))
		inputErr := binanceProxyInputError(err)
		require.ErrorContains(t, inputErr, tc.hint)
		require.False(t, strings.Contains(inputErr.Error(), "private-secret"))
		require.False(t, strings.Contains(inputErr.Error(), "proxy.invalid"))
	}
}
