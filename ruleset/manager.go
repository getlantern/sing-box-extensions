/*
Package ruleset provides functionality to add/remove filters to rulesets or enable/disable all rules
using the ruleset.

Currently, only supports [rule.LocalRuleSet] with only 1 [option.DefaultRule] and the following filter types:
  - domain
  - domainSuffix
  - domainKeyword
  - domainRegex
  - processName
  - packageName

JSON format example:

	{
		"version":3,
		"rules":[
			{
				"domain":["test.com", "test2.com", ... ],
				"domain_suffix":[".cn"],
				"domain_keyword":["test"],
				"domain_regex":["^stun\\..+"],
				"process_name":["chrome"],
				"package_name":["com.android.chrome"]
			}
		]
	}

Note: [Manager.Start] should be called after a box instance is created but before it is started to
ensure managed rulesets take the initial state into account.
*/
package ruleset

import (
	"context"
)

// Manager allows creating and retrieving [MutableRuleSet]s.
type Manager struct {
	ctx      context.Context
	rulesets map[string]*MutableRuleSet
}

func NewManager() *Manager {
	return &Manager{
		rulesets: make(map[string]*MutableRuleSet),
	}
}

// Start starts all the rulesets managed by the [Manager].
func (m *Manager) Start(ctx context.Context) error {
	m.ctx = ctx
	for _, r := range m.rulesets {
		if err := r.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// NewMutableRuleSet creates a new [MutableRuleSet] to manage the [adapter.RuleSet] with the provided
// tag. The returned [MutableRuleSet] will store [adapter.RuleSet] filters in a file named <tag>.json
// in the provided dataPath. enable specifies whether the ruleset is initially enabled. format should
// be either [constant.RuleSetFormatSource] (JSON) or [constant.RuleSetFormatBinary] (sing-box SRS).
func (m *Manager) NewMutableRuleSet(dataPath, tag, format string, enable bool) (*MutableRuleSet, error) {
	var err error
	m.rulesets[tag], err = newMutableRuleSet(dataPath, tag, format, enable)
	return m.rulesets[tag], err
}

// MutableRuleSet returns the [MutableRuleSet] with the provided tag.
func (m *Manager) MutableRuleSet(tag string) *MutableRuleSet {
	return m.rulesets[tag]
}
