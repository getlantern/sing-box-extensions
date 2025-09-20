package adapter

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	A "github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/experimental/clashapi/trafficontrol"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/logger"

	"github.com/getlantern/sing-box-extensions/adapter"
)

const (
	pollInterval = 5 * time.Second
	forceAfter   = 15 * time.Minute
)

type ConnectionManager interface {
	Connections() []trafficontrol.TrackerMetadata
}

type MutableGroupManager struct {
	outboundMgr A.OutboundManager
	endpointMgr A.EndpointManager
	groups      map[string]adapter.MutableOutboundGroup

	removalQueue *removalQueue
	closed       atomic.Bool
	mu           sync.Mutex
}

func NewMutableGroupManager(
	logger logger.ContextLogger,
	outboundManager A.OutboundManager,
	endpointManager A.EndpointManager,
	connectionManager ConnectionManager,
	groups []adapter.MutableOutboundGroup,
) *MutableGroupManager {
	gMap := make(map[string]adapter.MutableOutboundGroup)
	for _, group := range groups {
		gMap[group.Tag()] = group
	}
	return &MutableGroupManager{
		outboundMgr: outboundManager,
		endpointMgr: endpointManager,
		groups:      gMap,
		removalQueue: newRemovalQueue(
			logger, outboundManager, endpointManager, connectionManager, pollInterval, forceAfter,
		),
	}
}

func (m *MutableGroupManager) Close() {
	if m.closed.Swap(true) {
		return
	}
	m.removalQueue.stop()
}

func (m *MutableGroupManager) Groups() []adapter.MutableOutboundGroup {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Collect(maps.Values(m.groups))
}

// CreateOutboundForGroup creates an outbound for the specified group.
func (m *MutableGroupManager) CreateOutboundForGroup(
	ctx context.Context,
	router A.Router,
	logger log.ContextLogger,
	group, tag, typ string,
	options any,
) error {
	return m.createForGroup(ctx, m.outboundMgr, router, logger, group, tag, typ, options)
}

// CreateEndpointForGroup creates an endpoint for the specified group.
func (m *MutableGroupManager) CreateEndpointForGroup(
	ctx context.Context,
	router A.Router,
	logger log.ContextLogger,
	group, tag, typ string,
	options any,
) error {
	return m.createForGroup(ctx, m.endpointMgr, router, logger, group, tag, typ, options)
}

type outboundManager interface {
	Create(ctx context.Context, router A.Router, logger log.ContextLogger, tag string, outboundType string, options any) error
}

func (m *MutableGroupManager) createForGroup(
	ctx context.Context,
	mgr outboundManager,
	router A.Router,
	logger log.ContextLogger,
	group, tag, typ string,
	options any,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return errors.New("manager is closed")
	}

	groupObj, found := m.groups[group]
	if !found {
		return fmt.Errorf("group %s not found", group)
	}

	if err := mgr.Create(ctx, router, logger, tag, typ, options); err != nil {
		return err
	}
	n, err := groupObj.Add([]string{tag})
	if err != nil || n == 0 {
		if err == nil {
			err = errors.New("unknown")
		}
		return fmt.Errorf("failed to add %s to %s: %w", tag, group, err)
	}
	return nil
}

// CreateOutboundForGroup creates an outbound for the specified group.
func (m *MutableGroupManager) RemoveFromGroup(group, tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return errors.New("manager is closed")
	}
	groupObj, found := m.groups[group]
	if !found {
		return fmt.Errorf("group %s not found", group)
	}

	n, err := groupObj.Remove([]string{tag})
	if err != nil || n == 0 {
		if err == nil {
			err = errors.New("unknown")
		}
		return fmt.Errorf("failed to remove %s from %s: %w", tag, group, err)
	}

	_, isEndpoint := m.endpointMgr.Get(tag)
	m.removalQueue.add(tag, isEndpoint)
	return nil
}

// removalQueue handles delayed removal of outbounds/endpoints.
type removalQueue struct {
	logger       log.ContextLogger
	outMgr       A.OutboundManager
	epMgr        A.EndpointManager
	connMgr      ConnectionManager
	pending      map[string]item
	ticker       *time.Ticker
	pollInterval time.Duration
	forceAfter   time.Duration
	mu           sync.RWMutex
	done         chan struct{}
	closed       atomic.Bool
}

type item struct {
	tag        string
	isEndpoint bool
	addedAt    time.Time
}

func newRemovalQueue(
	logger log.ContextLogger,
	outMgr A.OutboundManager,
	epMgr A.EndpointManager,
	connMgr ConnectionManager,
	pollInterval, forceAfter time.Duration,
) *removalQueue {
	return &removalQueue{
		logger:       logger,
		outMgr:       outMgr,
		epMgr:        epMgr,
		connMgr:      connMgr,
		pending:      make(map[string]item),
		pollInterval: pollInterval,
		forceAfter:   forceAfter,
		done:         make(chan struct{}),
	}
}

func (rq *removalQueue) add(tag string, isEndpoint bool) {
	if rq.closed.Load() {
		return
	}
	rq.mu.Lock()
	defer rq.mu.Unlock()
	if _, exists := rq.pending[tag]; exists {
		return
	}
	rq.pending[tag] = item{
		tag:        tag,
		isEndpoint: isEndpoint,
		addedAt:    time.Now(),
	}
	if rq.ticker == nil {
		rq.ticker = time.NewTicker(rq.pollInterval)
		go rq.checkLoop()
	}
}

func (rq *removalQueue) checkLoop() {
	for {
		select {
		case <-rq.ticker.C:
			rq.checkPending()
		case <-rq.done:
			rq.ticker.Stop()
			return
		}
	}
}

// checkPending checks the pending removal items and removes them if they are not in use or have
// exceeded the forceAfter duration.
func (rq *removalQueue) checkPending() {
	rq.mu.RLock()
	pending := make(map[string]item, len(rq.pending))
	for tag, item := range rq.pending {
		pending[tag] = item
	}
	rq.mu.RUnlock()

	hasConns := make(map[string]bool, len(rq.pending))
	for _, conn := range rq.connMgr.Connections() {
		if _, exists := pending[conn.Outbound]; exists {
			hasConns[conn.Outbound] = hasConns[conn.Outbound] || !conn.ClosedAt.IsZero()
		}
	}

	var toRemove []string
	for tag, item := range pending {
		if !hasConns[tag] || time.Since(item.addedAt) > rq.forceAfter {
			toRemove = append(toRemove, tag)
		}
	}

	if len(toRemove) > 0 {
		rq.mu.Lock()
		defer rq.mu.Unlock()
		for _, tag := range toRemove {
			item, exists := rq.pending[tag]
			if !exists {
				continue
			}
			rq.logger.Debug("removing outbound", "tag", tag)
			if item.isEndpoint {
				rq.epMgr.Remove(tag)
			} else {
				rq.outMgr.Remove(tag)
			}
			delete(rq.pending, tag)
		}
		// Stop ticker if no pending items remain
		if len(rq.pending) == 0 && rq.ticker != nil {
			rq.ticker.Stop()
			rq.ticker = nil
		}
	}
}

func (rq *removalQueue) stop() {
	select {
	case <-rq.done:
	default:
		close(rq.done)
	}
}
