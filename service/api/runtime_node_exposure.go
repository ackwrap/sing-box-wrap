package api

import (
	"context"
	"crypto/subtle"
	stdJSON "encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
)

const (
	runtimeAPIPrefix             = "/api/v1/"
	runtimeNodeExposurePath      = runtimeAPIPrefix + "node-exposures"
	runtimeNodeExposureBodyLimit = 1 << 20
	runtimeNodeExposureTagPrefix = "ackwrap-node-exposure-"
)

var (
	errRuntimeAPIUnavailable       = errors.New("runtime API is unavailable")
	errRuntimeAPIConflict          = errors.New("runtime resource already exists")
	errRuntimeNodeExposureNotFound = errors.New("runtime node exposure not found")
)

type runtimeAPI struct {
	ctx             context.Context
	logger          log.ContextLogger
	secret          string
	inboundManager  adapter.InboundManager
	outboundManager adapter.OutboundManager
	router          adapter.Router
	runtimeRouter   adapter.RuntimeRouteManager

	access    sync.Mutex
	exposures map[string]runtimeNodeExposure
	closing   atomic.Bool
}

type runtimeNodeExposureRequest struct {
	Inbound     option.Inbound `json:"inbound"`
	OutboundTag string         `json:"outbound_tag"`
}

type runtimeNodeExposure struct {
	ID           string
	Inbound      option.Inbound
	OutboundTag  string
	OutboundType string
	Active       bool
}

type runtimeNodeExposureSummary struct {
	ID           string `json:"id"`
	InboundTag   string `json:"inbound_tag"`
	InboundType  string `json:"inbound_type"`
	OutboundTag  string `json:"outbound_tag"`
	OutboundType string `json:"outbound_type"`
}

type runtimeNodeExposureList struct {
	Items []runtimeNodeExposureSummary `json:"items"`
}

type runtimeAPIErrorEnvelope struct {
	Error runtimeAPIError `json:"error"`
}

type runtimeAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func newRuntimeAPI(ctx context.Context, logger log.ContextLogger, secret string) *runtimeAPI {
	router := service.FromContext[adapter.Router](ctx)
	runtimeRouter, _ := router.(adapter.RuntimeRouteManager)
	return &runtimeAPI{
		ctx:             ctx,
		logger:          logger,
		secret:          secret,
		inboundManager:  service.FromContext[adapter.InboundManager](ctx),
		outboundManager: service.FromContext[adapter.OutboundManager](ctx),
		router:          router,
		runtimeRouter:   runtimeRouter,
		exposures:       make(map[string]runtimeNodeExposure),
	}
}

func (a *runtimeAPI) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if !a.authenticate(request) {
		writer.Header().Set("WWW-Authenticate", "Bearer")
		writeRuntimeAPIError(writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid authorization")
		return
	}
	if request.URL.Path == runtimeAPIPrefix+"health" {
		if request.Method != http.MethodGet {
			writeRuntimeAPIError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		if !a.ready() {
			writeRuntimeAPIError(writer, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE", errRuntimeAPIUnavailable.Error())
			return
		}
		writeRuntimeAPIJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if request.URL.Path == runtimeRoutingPath {
		a.handleRuntimeRouting(writer, request)
		return
	}
	if request.URL.Path == runtimeAccessEventsPath {
		a.handleRuntimeAccessEvents(writer, request)
		return
	}
	if request.URL.Path == runtimeNodeExposurePath {
		if request.Method != http.MethodGet {
			writeRuntimeAPIError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		writeRuntimeAPIJSON(writer, http.StatusOK, a.list())
		return
	}
	pathID, found := strings.CutPrefix(request.URL.Path, runtimeNodeExposurePath+"/")
	if !found || !validRuntimeExposureID(pathID) {
		writeRuntimeAPIError(writer, http.StatusNotFound, "NOT_FOUND", "resource not found")
		return
	}
	switch request.Method {
	case http.MethodPut:
		a.handlePut(writer, request, pathID)
	case http.MethodDelete:
		a.handleDelete(writer, pathID)
	default:
		writeRuntimeAPIError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (a *runtimeAPI) handlePut(writer http.ResponseWriter, request *http.Request, id string) {
	if !a.ready() {
		writeRuntimeAPIError(writer, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE", errRuntimeAPIUnavailable.Error())
		return
	}
	content, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, runtimeNodeExposureBodyLimit))
	if err != nil {
		writeRuntimeAPIError(writer, http.StatusBadRequest, "INVALID_REQUEST", "request body is too large or unreadable")
		return
	}
	exposure, err := a.parseExposure(id, content)
	if err != nil {
		writeRuntimeAPIError(writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	err = a.upsert(exposure)
	if err != nil {
		if errors.Is(err, errRuntimeAPIUnavailable) {
			writeRuntimeAPIError(writer, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE", errRuntimeAPIUnavailable.Error())
			return
		}
		if errors.Is(err, errRuntimeAPIConflict) {
			writeRuntimeAPIError(writer, http.StatusConflict, "RESOURCE_CONFLICT", err.Error())
			return
		}
		writeRuntimeAPIError(writer, http.StatusInternalServerError, "RUNTIME_APPLY_FAILED", err.Error())
		return
	}
	writeRuntimeAPIJSON(writer, http.StatusOK, exposure.summary())
}

func (a *runtimeAPI) handleDelete(writer http.ResponseWriter, id string) {
	if !a.ready() {
		writeRuntimeAPIError(writer, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE", errRuntimeAPIUnavailable.Error())
		return
	}
	err := a.delete(id)
	if errors.Is(err, errRuntimeAPIUnavailable) {
		writeRuntimeAPIError(writer, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE", errRuntimeAPIUnavailable.Error())
		return
	}
	if errors.Is(err, errRuntimeNodeExposureNotFound) {
		writeRuntimeAPIError(writer, http.StatusNotFound, "NOT_FOUND", "node exposure not found")
		return
	}
	if err != nil {
		writeRuntimeAPIError(writer, http.StatusInternalServerError, "RUNTIME_APPLY_FAILED", err.Error())
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (a *runtimeAPI) ready() bool {
	return !a.closing.Load() && a.inboundManager != nil && a.outboundManager != nil && a.router != nil && a.runtimeRouter != nil
}

func (a *runtimeAPI) authenticate(request *http.Request) bool {
	if a.secret == "" {
		return false
	}
	token, found := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if !found || len(token) != len(a.secret) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(a.secret)) == 1
}

func (a *runtimeAPI) parseExposure(id string, content []byte) (runtimeNodeExposure, error) {
	request, err := json.UnmarshalExtendedContext[runtimeNodeExposureRequest](a.ctx, content)
	if err != nil {
		return runtimeNodeExposure{}, E.Cause(err, "decode node exposure")
	}
	request.OutboundTag = strings.TrimSpace(request.OutboundTag)
	if request.OutboundTag == "" {
		return runtimeNodeExposure{}, errors.New("missing outbound_tag")
	}
	switch request.Inbound.Type {
	case C.TypeHTTP, C.TypeSOCKS, C.TypeMixed:
	default:
		return runtimeNodeExposure{}, E.New("unsupported inbound type: ", request.Inbound.Type)
	}
	listenWrapper, loaded := request.Inbound.Options.(option.ListenOptionsWrapper)
	if !loaded {
		return runtimeNodeExposure{}, errors.New("inbound does not support listen options")
	}
	listenOptions := listenWrapper.TakeListenOptions()
	if listenOptions.ListenPort == 0 {
		return runtimeNodeExposure{}, errors.New("listen_port must be greater than zero")
	}
	if listenOptions.Detour != "" {
		return runtimeNodeExposure{}, errors.New("inbound detour is not supported")
	}
	users, setSystemProxy := runtimeInboundSecurity(request.Inbound.Options)
	if setSystemProxy {
		return runtimeNodeExposure{}, errors.New("set_system_proxy is not supported")
	}
	if users == 0 && (listenOptions.Listen == nil || !netip.Addr(*listenOptions.Listen).IsLoopback()) {
		return runtimeNodeExposure{}, errors.New("authentication is required for non-loopback listeners")
	}
	request.Inbound.Tag = runtimeNodeExposureTagPrefix + "in-" + id
	targetOutbound, loaded := a.outboundManager.Outbound(request.OutboundTag)
	if !loaded {
		return runtimeNodeExposure{}, E.New("outbound not found: ", request.OutboundTag)
	}
	return runtimeNodeExposure{
		ID:           id,
		Inbound:      request.Inbound,
		OutboundTag:  request.OutboundTag,
		OutboundType: targetOutbound.Type(),
	}, nil
}

func runtimeInboundSecurity(options any) (users int, setSystemProxy bool) {
	switch inboundOptions := options.(type) {
	case *option.SocksInboundOptions:
		return len(inboundOptions.Users), false
	case *option.HTTPMixedInboundOptions:
		return len(inboundOptions.Users), inboundOptions.SetSystemProxy
	default:
		return 0, false
	}
}

func validRuntimeExposureID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for index, character := range id {
		isLetter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		isDigit := character >= '0' && character <= '9'
		isSeparator := index > 0 && (character == '-' || character == '_')
		if isLetter || isDigit || isSeparator {
			continue
		}
		return false
	}
	return true
}

func (a *runtimeAPI) list() runtimeNodeExposureList {
	a.access.Lock()
	items := make([]runtimeNodeExposureSummary, 0, len(a.exposures))
	for _, exposure := range a.exposures {
		if exposure.Active {
			items = append(items, exposure.summary())
		}
	}
	a.access.Unlock()
	sort.Slice(items, func(i int, j int) bool { return items[i].ID < items[j].ID })
	return runtimeNodeExposureList{Items: items}
}

func (a *runtimeAPI) upsert(exposure runtimeNodeExposure) error {
	if a.closing.Load() {
		return errRuntimeAPIUnavailable
	}
	a.access.Lock()
	defer a.access.Unlock()
	if a.closing.Load() {
		return errRuntimeAPIUnavailable
	}
	existing, loaded := a.exposures[exposure.ID]
	if loaded {
		if !existing.Active {
			if err := a.cleanupInactiveLocked(existing); err != nil {
				return E.Cause(err, "clean incomplete runtime exposure")
			}
			delete(a.exposures, existing.ID)
			return a.createLocked(exposure)
		}
		return a.updateLocked(existing, exposure)
	}
	return a.createLocked(exposure)
}

func (a *runtimeAPI) createLocked(exposure runtimeNodeExposure) error {
	if _, loaded := a.inboundManager.Get(exposure.Inbound.Tag); loaded {
		return E.Cause(errRuntimeAPIConflict, "inbound tag ", exposure.Inbound.Tag)
	}
	if err := a.validateTarget(exposure); err != nil {
		return err
	}
	a.runtimeRouter.SetRuntimeInboundOutbound(exposure.Inbound.Tag, exposure.OutboundTag)
	if err := a.createInbound(exposure); err != nil {
		a.runtimeRouter.RemoveRuntimeInboundOutbound(exposure.Inbound.Tag)
		return E.Cause(err, "create runtime inbound")
	}
	exposure.Active = true
	a.exposures[exposure.ID] = exposure
	a.logger.Info("runtime node exposure created: ", exposure.ID, " (", exposure.Inbound.Type, " -> ", exposure.OutboundType, ")")
	return nil
}

func (a *runtimeAPI) updateLocked(existing runtimeNodeExposure, exposure runtimeNodeExposure) error {
	if err := a.inboundManager.Remove(existing.Inbound.Tag); err != nil {
		return a.restoreLocked(existing, E.Cause(err, "remove previous runtime inbound"))
	}
	if err := a.validateTarget(exposure); err != nil {
		return a.restoreLocked(existing, err)
	}
	a.runtimeRouter.SetRuntimeInboundOutbound(exposure.Inbound.Tag, exposure.OutboundTag)
	if err := a.createInbound(exposure); err != nil {
		return a.restoreLocked(existing, E.Cause(err, "create replacement runtime inbound"))
	}
	exposure.Active = true
	a.exposures[exposure.ID] = exposure
	a.logger.Info("runtime node exposure updated: ", exposure.ID, " (", exposure.Inbound.Type, " -> ", exposure.OutboundType, ")")
	return nil
}

func (a *runtimeAPI) delete(id string) error {
	if a.closing.Load() {
		return errRuntimeAPIUnavailable
	}
	a.access.Lock()
	defer a.access.Unlock()
	if a.closing.Load() {
		return errRuntimeAPIUnavailable
	}
	existing, loaded := a.exposures[id]
	if !loaded {
		return errRuntimeNodeExposureNotFound
	}
	if !existing.Active {
		if err := a.cleanupInactiveLocked(existing); err != nil {
			return E.Cause(err, "clean incomplete runtime exposure")
		}
		delete(a.exposures, id)
		return nil
	}
	if err := a.inboundManager.Remove(existing.Inbound.Tag); err != nil {
		return a.restoreLocked(existing, E.Cause(err, "remove runtime inbound"))
	}
	a.runtimeRouter.RemoveRuntimeInboundOutbound(existing.Inbound.Tag)
	delete(a.exposures, id)
	a.logger.Info("runtime node exposure deleted: ", id)
	return nil
}

func (a *runtimeAPI) Close() error {
	a.closing.Store(true)
	a.access.Lock()
	defer a.access.Unlock()
	var closeErr error
	for id, exposure := range a.exposures {
		cleanupErr := a.cleanupInactiveLocked(exposure)
		if cleanupErr != nil {
			exposure.Active = false
			a.exposures[id] = exposure
			closeErr = errors.Join(closeErr, E.Cause(cleanupErr, "close runtime node exposure ", id))
			continue
		}
		delete(a.exposures, id)
	}
	return closeErr
}

func (a *runtimeAPI) restoreLocked(existing runtimeNodeExposure, operationErr error) error {
	a.runtimeRouter.SetRuntimeInboundOutbound(existing.Inbound.Tag, existing.OutboundTag)
	rollbackErr := a.ensureInbound(existing)
	existing.Active = rollbackErr == nil
	a.exposures[existing.ID] = existing
	return runtimeRollbackError(operationErr, rollbackErr)
}

func (a *runtimeAPI) cleanupInactiveLocked(exposure runtimeNodeExposure) error {
	cleanupErr := a.removeInboundIfPresent(exposure.Inbound.Tag)
	if cleanupErr == nil {
		a.runtimeRouter.RemoveRuntimeInboundOutbound(exposure.Inbound.Tag)
	}
	return cleanupErr
}

func (a *runtimeAPI) ensureInbound(exposure runtimeNodeExposure) error {
	if _, loaded := a.inboundManager.Get(exposure.Inbound.Tag); loaded {
		return nil
	}
	return a.createInbound(exposure)
}

func (a *runtimeAPI) removeInboundIfPresent(tag string) error {
	if _, loaded := a.inboundManager.Get(tag); !loaded {
		return nil
	}
	return a.inboundManager.Remove(tag)
}

func (a *runtimeAPI) createInbound(exposure runtimeNodeExposure) error {
	return a.inboundManager.Create(a.ctx, a.router, a.logger, exposure.Inbound.Tag, exposure.Inbound.Type, exposure.Inbound.Options)
}

func (a *runtimeAPI) validateTarget(exposure runtimeNodeExposure) error {
	if _, loaded := a.outboundManager.Outbound(exposure.OutboundTag); !loaded {
		return E.New("runtime outbound not found: ", exposure.OutboundTag)
	}
	return nil
}

func (e runtimeNodeExposure) summary() runtimeNodeExposureSummary {
	return runtimeNodeExposureSummary{
		ID:           e.ID,
		InboundTag:   e.Inbound.Tag,
		InboundType:  e.Inbound.Type,
		OutboundTag:  e.OutboundTag,
		OutboundType: e.OutboundType,
	}
}

func runtimeRollbackError(operationErr error, rollbackErr error) error {
	if rollbackErr == nil {
		return operationErr
	}
	return fmt.Errorf("%w; rollback failed: %v", operationErr, rollbackErr)
}

func writeRuntimeAPIError(writer http.ResponseWriter, statusCode int, code string, message string) {
	writeRuntimeAPIJSON(writer, statusCode, runtimeAPIErrorEnvelope{Error: runtimeAPIError{Code: code, Message: message}})
}

func writeRuntimeAPIJSON(writer http.ResponseWriter, statusCode int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_ = stdJSON.NewEncoder(writer).Encode(value)
}
