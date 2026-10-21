package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeRechargeTabs(t *testing.T) {
	t.Run("empty restores default", func(t *testing.T) {
		tabs, err := NormalizeRechargeTabs(nil)
		require.NoError(t, err)
		require.Empty(t, tabs)
	})

	t.Run("normalizes, dedups methods and sorts", func(t *testing.T) {
		tabs, err := NormalizeRechargeTabs([]RechargeTab{
			{ID: " Binance ", Name: " 币安支付 ", SortOrder: 2, Enabled: true, Methods: []string{"binance", "Binance"}},
			{ID: "recharge", Name: "充值", SortOrder: 1, Enabled: true, Methods: []string{"alipay", "wxpay"}},
			{ID: "usdc", Name: "USDC", SortOrder: 3, Enabled: false},
		})
		require.NoError(t, err)
		require.Equal(t, []RechargeTab{
			{ID: "recharge", Name: "充值", SortOrder: 1, Enabled: true, Methods: []string{"alipay", "wxpay"}},
			{ID: "binance", Name: "币安支付", SortOrder: 2, Enabled: true, Methods: []string{"binance"}},
			{ID: "usdc", Name: "USDC", SortOrder: 3, Enabled: false, Methods: []string{}},
		}, tabs)
	})

	cases := map[string][]RechargeTab{
		"bad id":                 {{ID: "有", Name: "x", Enabled: true, Methods: []string{"alipay"}}},
		"duplicate id":           {{ID: "a", Name: "x", Enabled: true, Methods: []string{"alipay"}}, {ID: "a", Name: "y", Enabled: true, Methods: []string{"wxpay"}}},
		"empty name":             {{ID: "a", Name: "  ", Enabled: true, Methods: []string{"alipay"}}},
		"long name":              {{ID: "a", Name: "一二三四五六七八九十一二三四五六七八九十一二三四五六七八九十一二三", Enabled: true, Methods: []string{"alipay"}}},
		"enabled without method": {{ID: "a", Name: "x", Enabled: true}},
		"bad method":             {{ID: "a", Name: "x", Enabled: true, Methods: []string{"a b"}}},
		"none enabled":           {{ID: "a", Name: "x", Enabled: false, Methods: []string{"alipay"}}},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NormalizeRechargeTabs(input)
			require.Error(t, err)
		})
	}

	t.Run("too many tabs", func(t *testing.T) {
		many := make([]RechargeTab, maxRechargeTabs+1)
		for i := range many {
			many[i] = RechargeTab{ID: string(rune('a' + i)), Name: "x", Enabled: true, Methods: []string{"alipay"}}
		}
		_, err := NormalizeRechargeTabs(many)
		require.Error(t, err)
	})
}

func TestParseRechargeTabsLenient(t *testing.T) {
	require.Empty(t, parseRechargeTabs(""))
	require.Empty(t, parseRechargeTabs("{not json"))
	// All enabled tabs invalid -> default.
	require.Empty(t, parseRechargeTabs(`[{"id":"a","name":"","enabled":true,"methods":["alipay"]}]`))

	tabs := parseRechargeTabs(`[{"id":"b","name":"B","sort_order":5,"enabled":true,"methods":["binance"]},{"id":"bad id","name":"x","enabled":true,"methods":["x"]},{"id":"a","name":"A","sort_order":1,"enabled":true,"methods":["alipay"]},{"id":"a","name":"dup","enabled":true,"methods":["wxpay"]},{"id":"c","name":"C","enabled":true,"methods":[]}]`)
	require.Equal(t, []RechargeTab{
		{ID: "c", Name: "C", SortOrder: 0, Enabled: false, Methods: []string{}},
		{ID: "a", Name: "A", SortOrder: 1, Enabled: true, Methods: []string{"alipay"}},
		{ID: "b", Name: "B", SortOrder: 5, Enabled: true, Methods: []string{"binance"}},
	}, tabs)
	require.Equal(t, []RechargeTab{tabs[1], tabs[2]}, EnabledRechargeTabs(tabs))
}

func TestUpdatePaymentConfigRechargeTabs(t *testing.T) {
	ctx := context.Background()

	t.Run("persists and reads back", func(t *testing.T) {
		repo := &paymentConfigSettingRepoStub{values: map[string]string{}}
		svc := &PaymentConfigService{settingRepo: repo}
		tabs := []RechargeTab{
			{ID: "binance", Name: "币安支付", SortOrder: 2, Enabled: true, Methods: []string{"binance"}},
			{ID: "recharge", Name: "充值", SortOrder: 1, Enabled: true, Methods: []string{"alipay"}},
		}
		require.NoError(t, svc.UpdatePaymentConfig(ctx, UpdatePaymentConfigRequest{RechargeTabs: &tabs}))
		require.Equal(t, `[{"id":"recharge","name":"充值","sort_order":1,"enabled":true,"methods":["alipay"]},{"id":"binance","name":"币安支付","sort_order":2,"enabled":true,"methods":["binance"]}]`, repo.updates[SettingRechargeTabs])

		cfg, err := svc.GetPaymentConfig(ctx)
		require.NoError(t, err)
		require.Len(t, cfg.RechargeTabs, 2)
		require.Equal(t, "recharge", cfg.RechargeTabs[0].ID)
	})

	t.Run("empty clears, omitted is untouched", func(t *testing.T) {
		repo := &paymentConfigSettingRepoStub{values: map[string]string{SettingRechargeTabs: `[{"id":"a","name":"A","enabled":true,"methods":["alipay"]}]`}}
		svc := &PaymentConfigService{settingRepo: repo}
		notice := "x"
		require.NoError(t, svc.UpdatePaymentConfig(ctx, UpdatePaymentConfigRequest{RechargeBonusNotice: &notice}))
		_, touched := repo.updates[SettingRechargeTabs]
		require.False(t, touched)

		empty := []RechargeTab{}
		require.NoError(t, svc.UpdatePaymentConfig(ctx, UpdatePaymentConfigRequest{RechargeTabs: &empty}))
		value, ok := repo.updates[SettingRechargeTabs]
		require.True(t, ok)
		require.Equal(t, "", value)
	})

	t.Run("rejects invalid", func(t *testing.T) {
		repo := &paymentConfigSettingRepoStub{values: map[string]string{}}
		svc := &PaymentConfigService{settingRepo: repo}
		bad := []RechargeTab{{ID: "a", Name: "A", Enabled: true}}
		err := svc.UpdatePaymentConfig(ctx, UpdatePaymentConfigRequest{RechargeTabs: &bad})
		require.Error(t, err)
		require.Nil(t, repo.updates)
	})
}
