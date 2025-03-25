package ruleset

import (
	"context"
	"net/netip"
	"os"
	"testing"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMutableRuleSet(t *testing.T) {
	rsTag := "rule-set"
	domain := "ipconfig.io"

	ctx, instance, path := setup(t, rsTag, domain)
	defer os.RemoveAll(path)

	_, loaded := instance.Router().RuleSet(rsTag)
	require.True(t, loaded, "ruleset not loaded")

	// start the router rule. this would normally be done by the router itself when it starts
	rules := instance.Router().Rules()
	rsRule := rules[0]
	rsRule.Start()

	inboundCtx := &adapter.InboundContext{
		IPVersion: 4,
		Domain:    domain,
	}

	m := newMutableRuleSet(path, rsTag, "source", true)
	reset := func() {
		m.filter.Domain = []string{domain}
		m.Enable()
	}

	testStart(t, ctx, m, rsTag, domain)

	matchTests := []struct {
		name    string
		alterFn func(*MutableRuleSet) *adapter.InboundContext
		want    bool
	}{
		{
			name: "disable",
			alterFn: func(mrs *MutableRuleSet) *adapter.InboundContext {
				mrs.Disable()
				return inboundCtx
			},
			want: false,
		},
		{
			name: "re-enable",
			alterFn: func(mrs *MutableRuleSet) *adapter.InboundContext {
				mrs.Disable()
				mrs.Enable()
				return inboundCtx
			},
			want: true,
		},
		{
			name: "match added item",
			alterFn: func(mrs *MutableRuleSet) *adapter.InboundContext {
				mrs.AddItem(TypeDomain, "google.com")
				inboundCtx.Domain = "google.com"
				return inboundCtx
			},
			want: true,
		},
		{
			name: "should not match removed item",
			alterFn: func(mrs *MutableRuleSet) *adapter.InboundContext {
				mrs.RemoveItem(TypeDomain, inboundCtx.Domain)
				return inboundCtx
			},
			want: true,
		},
	}
	for _, tt := range matchTests {
		t.Run(tt.name, func(t *testing.T) {
			reset()
			testMatch(t, instance, m, tt.alterFn, inboundCtx, tt.want)
		})
	}
}

func setup(t *testing.T, rsTag, domain string) (context.Context, *box.Box, string) {
	path, err := os.MkdirTemp("", "test")
	require.NoError(t, err)
	rsFile := path + "/" + rsTag + ".json"
	err = os.WriteFile(rsFile, []byte(`{"version":3,"rules":[{"domain":"`+domain+`"}]}`), 0644)
	require.NoError(t, err, "failed to create rule file")

	ctx := box.Context(context.Background(), include.InboundRegistry(), include.OutboundRegistry(), include.EndpointRegistry())

	instance, err := box.New(box.Options{
		Context: ctx,
		Options: testOptions(rsTag, rsFile),
	})
	require.NoError(t, err, "failed to create box instance")
	return ctx, instance, path
}

func testStart(t *testing.T, ctx context.Context, mRuleSet *MutableRuleSet, rsTag, domain string) {
	require.NoError(t, mRuleSet.Start(ctx), "Start failed")
	require.Len(t, mRuleSet.rules, 1, "rules not loaded")

	rule := mRuleSet.rules[0].(*ruleWrapper)
	require.Equal(t, rule.name, rsTag, "rule name mismatch")
	require.Contains(t, mRuleSet.filter.Domain, domain, "rule not loaded")
}

func testMatch(
	t *testing.T,
	instance *box.Box,
	mrs *MutableRuleSet,
	alter func(*MutableRuleSet) *adapter.InboundContext,
	inboundCtx *adapter.InboundContext,
	stillMatch bool,
) {
	router := instance.Router()
	rules := router.Rules()
	require.Len(t, rules, 1, "rules not loaded")
	rsOriginal := rules[0]
	assert.True(t, rsOriginal.Match(inboundCtx), "original rule match failed")

	alterInboundCtx := alter(mrs)
	router = instance.Router()
	rules = router.Rules()
	require.Len(t, rules, 1, "rules not loaded")
	rsAltered := rules[0]
	assert.Equalf(t, stillMatch, rsAltered.Match(alterInboundCtx),
		"altered rule match failed\nruleset: original[%v], altered[%v]\ninboundCtx: original[%v], altered[%v]",
		rsOriginal.String(), rsAltered.String(), inboundCtx, alterInboundCtx,
	)
}

func TestAddRemoveItems(t *testing.T) {
	path, err := os.MkdirTemp("", "test")
	require.NoError(t, err)
	defer os.RemoveAll(path)

	m := newMutableRuleSet(path, "test", "source", false)

	// test AddItem
	flen := len(m.filter.Domain)
	assert.NoError(t, m.AddItem(TypeDomain, "example.com"), "AddItem failed")
	require.Len(t, m.filter.Domain, flen+1, "item not added")
	assert.Contains(t, m.filter.Domain, "example.com", "item not added")

	flen = len(m.filter.Domain)
	assert.NoError(t, m.AddItem(TypeDomain, "google.com"), "AddItem failed")
	require.Len(t, m.filter.Domain, flen+1, "item not added")
	assert.Contains(t, m.filter.Domain, "google.com", "item not added")

	t.Log(m.Filters())
	assert.Error(t, m.AddItem("unsupportedType", "example.com"), "AddItem should have failed")

	// test RemoveItem
	assert.NoError(t, m.RemoveItem(TypeDomain, "example.com"), "RemoveItem failed")
	assert.NotContains(t, m.filter.Domain, "example.com", "item not removed")

	assert.Error(t, m.RemoveItem("unsupportedType", "example.com"), "RemoveItem should have failed")
}

func testOptions(rsTag, rsPath string) option.Options {
	opts := option.Options{
		Log: &option.LogOptions{Disabled: true},
		Inbounds: []option.Inbound{
			{
				Type: constant.TypeHTTP,
				Tag:  "http-in",
				Options: &option.HTTPMixedInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.MustParseAddr("127.0.0.1"))),
						ListenPort: 3003,
					},
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: constant.TypeDirect,
			},
			{
				Type: constant.TypeHTTP,
				Tag:  "http-out",
				Options: &option.HTTPOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     "127.0.0.1",
						ServerPort: 3000,
					},
				},
			},
		},
		Route: &option.RouteOptions{
			Rules: []option.Rule{
				{
					Type: constant.RuleTypeDefault,
					DefaultOptions: option.DefaultRule{
						RawDefaultRule: option.RawDefaultRule{
							RuleSet: badoption.Listable[string]{rsTag},
						},
						RuleAction: option.RuleAction{
							Action: constant.RuleActionTypeRoute,
							RouteOptions: option.RouteActionOptions{
								Outbound: "http-out",
							},
						},
					},
				},
			},
			RuleSet: []option.RuleSet{
				{
					Type: constant.RuleSetTypeLocal,
					Tag:  rsTag,
					LocalOptions: option.LocalRuleSet{
						Path: rsPath,
					},
					Format: constant.RuleSetFormatSource,
				},
			},
		},
	}
	return opts
}
