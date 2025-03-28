package ruleset

import (
	"github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

// BaseRouteRule returns a base routing [option.Rule] config with the ruleset and an outboundTag set.
// Additional conditions can be added as needed.
func BaseRouteRule(rulesetTag, outboundTag string) option.Rule {
	return option.Rule{
		Type: constant.RuleTypeDefault,
		DefaultOptions: option.DefaultRule{
			RawDefaultRule: option.RawDefaultRule{
				RuleSet: badoption.Listable[string]{rulesetTag},
			},
			RuleAction: option.RuleAction{
				Action: constant.RuleActionTypeRoute,
				RouteOptions: option.RouteActionOptions{
					Outbound: outboundTag,
				},
			},
		},
	}
}

// LocalRuleSet returns a [option.RuleSet] of [constant.RuleSetTypeLocal] configured with the given
// tag, path, and format. The format should be either [constant.RuleSetFormatSource] or
// [constant.RuleSetFormatBinary].
func LocalRuleSet(tag, path, format string) option.RuleSet {
	return option.RuleSet{
		Type: constant.RuleSetTypeLocal,
		Tag:  tag,
		LocalOptions: option.LocalRuleSet{
			Path: path,
		},
		Format: format,
	}
}
