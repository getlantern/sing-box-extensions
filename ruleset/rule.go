package ruleset

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/srs"
	"github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
)

// Constants representing different types of filters currently supported.
const (
	TypeDomain        = "domain"
	TypeDomainSuffix  = "domainSuffix"
	TypeDomainKeyword = "domainKeyword"
	TypeDomainRegex   = "domainRegex"
	TypeProcessName   = "processName"
	TypePackageName   = "packageName"
)

// MutableRuleSet allows adding/removing items to/from an [adapter.RuleSet] filter and the ability
// enable/disable all rules currently using the ruleset.
type MutableRuleSet struct {
	tag     string
	enabled *atomic.Bool

	router   adapter.Router
	rules    []adapter.Rule
	filter   option.DefaultHeadlessRule
	filterMu sync.RWMutex
	started  atomic.Bool

	ruleFile string
	// fileFormat is the format of the rule file.
	fileFormat string
}

// newMutableRuleSet returns a new [MutableRuleSet] with the given tag, dataPath, and enable state.
// format should be either [constant.RuleSetFormatSource] or [constant.RuleSetFormatBinary].
func newMutableRuleSet(dataPath, tag, format string, enable bool) *MutableRuleSet {
	// TODO: accept logger? Probably should..
	path := filepath.Join(dataPath, tag+".json")
	enabled := new(atomic.Bool)
	enabled.Store(enable)
	return &MutableRuleSet{
		tag:        tag,
		enabled:    enabled,
		started:    atomic.Bool{},
		ruleFile:   path,
		fileFormat: format,
	}
}

// Enable enables all rules using the managed ruleset
func (m *MutableRuleSet) Enable() {
	m.enabled.Store(true)
}

// Disable disables all rules using the managed ruleset
func (m *MutableRuleSet) Disable() {
	m.enabled.Store(false)
}

func (m *MutableRuleSet) IsEnabled() bool {
	return m.enabled.Load()
}

// Start initializes the [MutableRuleSet] with the given context.
// Note, because [MutableRuleSet] uses the [adapter.Router] to find rules associated with the ruleset,
// the [adapter.Router] must be created and added to ctx before calling Start. This is typically done
// when creating a [boxService.BoxService].
func (m *MutableRuleSet) Start(ctx context.Context) error {
	if !m.started.CompareAndSwap(false, true) {
		return nil
	}

	router := service.FromContext[adapter.Router](ctx)
	ruleset, loaded := router.RuleSet(m.tag)
	if !loaded {
		return fmt.Errorf("%v ruleSet not found", m.tag)
	}
	if _, ok := ruleset.(*rule.LocalRuleSet); !ok {
		return fmt.Errorf("unsupported ruleset type: %T", ruleset)
	}

	// find all rules associated with the ruleset, wrap them, and then insert ourselves back into
	// the router. we also keep a reference to the rules to allow for enabling/disabling them.
	rules := router.Rules()
	for i, r := range rules {
		if strings.Contains(r.String(), "rule_set="+m.tag) {
			s := &ruleWrapper{
				Rule:    r,
				name:    m.tag,
				enabled: m.enabled,
			}
			rules[i] = s
			m.rules = append(m.rules, s)
		}
	}

	// load filters from the ruleset and register a callback to reload filters when the ruleset
	// changes so we're always in sync.
	ruleset.RegisterCallback(m.loadFilters)
	m.loadFilters(ruleset)
	return nil
}

// Filters returns the current filters as a JSON string.
func (m *MutableRuleSet) Filters() string {
	m.filterMu.RLock()
	defer m.filterMu.RUnlock()
	// we can safely ignore the error here because we know the filter is valid JSON
	buf, _ := json.Marshal(m.filter)
	return string(buf)
}

// AddItems adds multiple items to the filter. It returns an error if the filter type is unsupported
// or it fails to update the rule file.
func (m *MutableRuleSet) AddItems(filterType string, items []string) error {
	var errs []error
	for _, item := range items {
		if err := m.modify(filterType, item, add); err != nil {
			errs = append(errs, err)
		}
	}
	if err := m.saveToFile(); err != nil {
		return fmt.Errorf("writing rule to %s: %w", m.ruleFile, err)
	}
	return errors.Join(errs...)
}

// AddItem adds a new item to the filter of the given type. It returns an error if the filter type is
// unsupported or it fails to update the rule file.
func (m *MutableRuleSet) AddItem(filterType, item string) error {
	if err := m.modify(filterType, item, add); err != nil {
		return err
	}
	if err := m.saveToFile(); err != nil {
		m.modify(filterType, item, remove)
		return fmt.Errorf("writing rule to %s: %w", m.ruleFile, err)
	}
	return nil
}

// RemoveItems removes multiple items from the filter. It retruns an error if the filter type is
// unsupported or it fails to update the rule file.
func (m *MutableRuleSet) RemoveItems(filterType string, items []string) error {
	var errs []error
	for _, item := range items {
		if err := m.modify(filterType, item, remove); err != nil {
			errs = append(errs, err)
		}
	}
	if err := m.saveToFile(); err != nil {
		return fmt.Errorf("writing rule to %s: %w", m.ruleFile, err)
	}
	return errors.Join(errs...)
}

// RemoveItem removes an item from the filter of the given type. It retruns an error if the filter
// type is unsupported or it fails to update the rule file.
func (m *MutableRuleSet) RemoveItem(filterType, item string) error {
	if err := m.modify(filterType, item, remove); err != nil {
		return err
	}
	if err := m.saveToFile(); err != nil {
		return fmt.Errorf("writing rule to %s: %w", m.ruleFile, err)
	}
	return nil
}

type actionFn func(slice []string, item string) []string

func (m *MutableRuleSet) modify(filterType, item string, fn actionFn) error {
	m.filterMu.Lock()
	defer m.filterMu.Unlock()

	switch filterType {
	case TypeDomain:
		m.filter.Domain = fn(m.filter.Domain, item)
	case TypeDomainSuffix:
		m.filter.DomainSuffix = fn(m.filter.DomainSuffix, item)
	case TypeDomainKeyword:
		m.filter.DomainKeyword = fn(m.filter.DomainKeyword, item)
	case TypeDomainRegex:
		m.filter.DomainRegex = fn(m.filter.DomainRegex, item)
	case TypeProcessName:
		m.filter.ProcessName = fn(m.filter.ProcessName, item)
	case TypePackageName:
		m.filter.PackageName = fn(m.filter.PackageName, item)
	default:
		return fmt.Errorf("unsupported filter type: %s", filterType)
	}
	return nil
}

func add(slice []string, item string) []string {
	return append(slice, item)
}

func remove(slice []string, item string) []string {
	for i, v := range slice {
		if v == item {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

// saveToFile saves the current filter to the rule file.
func (m *MutableRuleSet) saveToFile() error {
	m.filterMu.RLock()
	filters := m.filter
	m.filterMu.RUnlock()
	switch m.fileFormat {
	case constant.RuleSetFormatSource, "":
		buf, err := json.Marshal(filters)
		if err != nil {
			return err
		}
		return os.WriteFile(m.ruleFile, buf, 0644)
	case constant.RuleSetFormatBinary:
		setFile, _ := os.Open(m.ruleFile)
		rs := option.PlainRuleSet{
			Rules: []option.HeadlessRule{
				{DefaultOptions: filters},
			},
		}
		return srs.Write(setFile, rs, 3)
	}
	return fmt.Errorf("unsupported rule file format: %s", m.fileFormat)
}

// loadFilters loads filters from the given [adapter.RuleSet].
func (m *MutableRuleSet) loadFilters(s adapter.RuleSet) {
	// we can safely ignore all errors in this function since the wrapped rule.LocalRuleSet would have
	// already failed before calling this function.
	var ruleSet option.PlainRuleSetCompat
	switch m.fileFormat {
	case constant.RuleSetFormatSource, "":
		content, _ := os.ReadFile(m.ruleFile)
		ruleSet, _ = json.UnmarshalExtended[option.PlainRuleSetCompat](content)
	case constant.RuleSetFormatBinary:
		setFile, _ := os.Open(m.ruleFile)
		ruleSet, _ = srs.Read(setFile, false)
	default:
		return
	}

	m.filterMu.Lock()
	m.filter = ruleSet.Options.Rules[0].DefaultOptions
	m.filterMu.Unlock()
}

// ruleWrapper wraps an [adapter.Rule] to allow checing if the rule is enabled before matching.
type ruleWrapper struct {
	// Rule is the underlying rule.
	adapter.Rule
	name    string
	enabled *atomic.Bool
}

// Match checks if the ruleset is enabled and the wrapped [adapter.Rule] matches metadata.
func (s *ruleWrapper) Match(metadata *adapter.InboundContext) bool {
	return s.enabled.Load() && s.Rule.Match(metadata)
}

// Type returns the rule type.
func (s *ruleWrapper) Type() string {
	return s.Rule.Type() + "-" + s.name
}

func (s *ruleWrapper) String() string {
	return fmt.Sprintf("%s enabled=%v", s.Rule.String(), s.enabled.Load())
}
