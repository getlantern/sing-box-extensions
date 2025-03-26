package ruleset

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
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

	rs, loaded := instance.Router().RuleSet(rsTag)
	require.True(t, loaded, "ruleset not loaded")
	rs.StartContext(ctx, nil)

	// start the router rule. this would normally be done by the router itself when it starts
	rules := instance.Router().Rules()
	rsRule := rules[0]
	rsRule.Start()

	inboundCtx := func(domain string) *adapter.InboundContext {
		return &adapter.InboundContext{
			Domain: domain,
		}
	}

	m, _ := newMutableRuleSet(path, rsTag, "source", true)
	reset := func() {
		m.filter.Domain = []string{domain}
		m.saveToFile()
		m.Enable()
	}

	testStart(t, ctx, m, rsTag, domain)

	matchTests := []struct {
		name    string
		alterFn func(*MutableRuleSet, chan struct{}) *adapter.InboundContext
		want    bool
	}{
		{
			name: "disable",
			alterFn: func(mrs *MutableRuleSet, _ chan struct{}) *adapter.InboundContext {
				mrs.Disable()
				return inboundCtx(domain)
			},
			want: false,
		},
		{
			name: "re-enable",
			alterFn: func(mrs *MutableRuleSet, _ chan struct{}) *adapter.InboundContext {
				mrs.Disable()
				mrs.Enable()
				return inboundCtx(domain)
			},
			want: true,
		},
		{
			name: "match added item",
			alterFn: func(mrs *MutableRuleSet, reloaded chan struct{}) *adapter.InboundContext {
				mrs.AddItem(TypeDomain, "google.com")
				<-reloaded
				return inboundCtx("google.com")
			},
			want: true,
		},
		{
			name: "should not match removed item",
			alterFn: func(mrs *MutableRuleSet, reloaded chan struct{}) *adapter.InboundContext {
				mrs.AddItems(TypeDomain, []string{"google.com", "example.com"})
				<-reloaded
				mrs.RemoveItem(TypeDomain, domain)
				<-reloaded
				return inboundCtx(domain)
			},
			want: false,
		},
	}
	for _, tt := range matchTests {
		t.Run(tt.name, func(t *testing.T) {
			reset()
			reloaded := make(chan struct{})
			cb := m.ruleset.RegisterCallback(func(it adapter.RuleSet) {
				reloaded <- struct{}{}
			})
			testMatch(t, instance, m, tt.alterFn, reloaded, inboundCtx(domain), tt.want)
			m.ruleset.UnregisterCallback(cb)
		})
	}
}

func setup(t *testing.T, rsTag, domain string) (context.Context, *box.Box, string) {
	path, err := os.MkdirTemp("", "test")
	require.NoError(t, err)
	rsFile := filepath.Join(path, rsTag+".json")
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
	alter func(*MutableRuleSet, chan struct{}) *adapter.InboundContext,
	reloaded chan struct{},
	inboundCtx *adapter.InboundContext,
	matchAltered bool,
) {
	router := instance.Router()
	rules := router.Rules()
	require.Len(t, rules, 1, "rules not loaded")
	assert.True(t, rules[0].Match(inboundCtx), "original rule match failed")

	ruleOriginal := rules[0].String()
	rsOriginal := mrs.ruleset.String()

	alterInboundCtx := alter(mrs, reloaded)
	router = instance.Router()
	rules = router.Rules()
	require.Len(t, rules, 1, "rules not loaded")

	ruleAltered := rules[0].String()
	rsAltered := mrs.ruleset.String()
	fmtErr := func() string {
		return fmt.Sprintf("rule:\n\toriginal[%v]\n\taltered[%v]", ruleOriginal, ruleAltered) +
			fmt.Sprintf("\nruleset:\n\toriginal[%v]\n\taltered[%v]", rsOriginal, rsAltered) +
			fmt.Sprintf("\ninbound domain:\n\toriginal[%v]\n\taltered[%v]",
				inboundCtx.Domain, alterInboundCtx.Domain,
			)
	}
	assert.Equalf(t, matchAltered, rules[0].Match(alterInboundCtx), "altered rule match failed\n"+fmtErr())
}

func TestAddRemoveItems(t *testing.T) {
	path, err := os.MkdirTemp("", "test")
	require.NoError(t, err)
	defer os.RemoveAll(path)

	m, _ := newMutableRuleSet(path, "test", "source", false)

	// test AddItem
	flen := len(m.filter.Domain)
	assert.NoError(t, m.AddItem(TypeDomain, "example.com"), "AddItem failed")
	m.loadFilters(nil)
	require.Len(t, m.filter.Domain, flen+1, "item not added")
	assert.Contains(t, m.filter.Domain, "example.com", "item not added")

	flen = len(m.filter.Domain)
	assert.NoError(t, m.AddItem(TypeDomain, "google.com"), "AddItem failed")
	m.loadFilters(nil)
	require.Len(t, m.filter.Domain, flen+1, "item not added")
	assert.Contains(t, m.filter.Domain, "google.com", "item not added")

	t.Log(m.Filters())
	assert.Error(t, m.AddItem("unsupportedType", "example.com"), "AddItem should have failed")

	// test RemoveItem
	assert.NoError(t, m.RemoveItem(TypeDomain, "example.com"), "RemoveItem failed")
	m.loadFilters(nil)
	assert.NotContains(t, m.filter.Domain, "example.com", "item not removed")

	assert.Error(t, m.RemoveItem("unsupportedType", "example.com"), "RemoveItem should have failed")
}

func testOptions(rsTag, rsPath string) option.Options {
	opts := option.Options{
		Log: &option.LogOptions{
			Disabled: false,
			Output:   "stdout",
		},
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
				BaseRouteRule(rsTag, "http-out"),
			},
			RuleSet: []option.RuleSet{
				LocalRuleSet(rsTag, rsPath, constant.RuleSetFormatSource),
			},
		},
	}
	return opts
}
