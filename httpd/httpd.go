package httpd

import (
	"dpasswd/fsm"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dgraph-io/badger/v2"
	"github.com/hashicorp/raft"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type raftHandler struct {
	raft *raft.Raft
}
type raftJoinRequest struct {
	NodeID      string `json:"node_id"`
	RaftAddress string `json:"raft_address"`
}
type raftRemoveRequest struct {
	NodeID string `json:"node_id"`
}

func NewRaftHandler(raft *raft.Raft) *raftHandler {
	return &raftHandler{
		raft: raft,
	}
}

func (rh raftHandler) Join(eCtx echo.Context) error {
	var req = raftJoinRequest{}
	if err := eCtx.Bind(&req); err != nil {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("error binding: %s", err.Error()),
		})
	}

	if rh.raft.State() != raft.Leader {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": "not the leader",
		})
	}

	configFuture := rh.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("failed to get raft configuration: %s", err.Error()),
		})
	}

	f := rh.raft.AddVoter(raft.ServerID(req.NodeID), raft.ServerAddress(req.RaftAddress), 0, 0)
	if f.Error() != nil {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("error add voter: %s", f.Error().Error()),
		})
	}

	return eCtx.JSON(http.StatusOK, map[string]interface{}{
		"message": fmt.Sprintf("node %s at %s joined successfully", req.NodeID, req.RaftAddress),
		"data":    rh.raft.Stats(),
	})
}
func (rh raftHandler) Remove(eCtx echo.Context) error {
	var req = raftRemoveRequest{}
	if err := eCtx.Bind(&req); err != nil {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("error binding: %s", err.Error()),
		})
	}

	if rh.raft.State() != raft.Leader {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": "not the leader",
		})
	}

	configFuture := rh.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("failed to get raft configuration: %s", err.Error()),
		})
	}

	future := rh.raft.RemoveServer(raft.ServerID(req.NodeID), 0, 0)
	if err := future.Error(); err != nil {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("error removing existing node %s: %s", req.NodeID, err.Error()),
		})
	}

	return eCtx.JSON(http.StatusOK, map[string]interface{}{
		"message": fmt.Sprintf("node %s removed successfully", req.NodeID),
		"data":    rh.raft.Stats(),
	})
}
func (rh raftHandler) Stats(eCtx echo.Context) error {
	return eCtx.JSON(http.StatusOK, map[string]interface{}{
		"message": "raft cluster status",
		"data":    rh.raft.Stats(),
	})
}

type fsmHandler struct {
	raft *raft.Raft
	db   *badger.DB
}
type fsmSetRequest struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

func NewFSMHandler(raft *raft.Raft, db *badger.DB) *fsmHandler {
	return &fsmHandler{
		raft: raft,
		db:   db,
	}
}

func (fh fsmHandler) Set(eCtx echo.Context) error {
	var req = fsmSetRequest{}
	if err := eCtx.Bind(&req); err != nil {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("error binding: %s", err.Error()),
		})
	}

	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": "key is empty",
		})
	}

	if fh.raft.State() != raft.Leader {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": "not the leader",
		})
	}

	payload := fsm.CommandPayload{
		Operation: "SET",
		Key:       req.Key,
		Value:     req.Value,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("error preparing saving data payload: %s", err.Error()),
		})
	}

	applyFuture := fh.raft.Apply(data, 500*time.Millisecond)
	if err := applyFuture.Error(); err != nil {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("error applying data to raft cluster: %s", err.Error()),
		})
	}

	_, ok := applyFuture.Response().(*fsm.ApplyResponse)
	if !ok {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("error response is not match apply response"),
		})
	}

	return eCtx.JSON(http.StatusOK, map[string]interface{}{
		"message": "data stored successfully",
		"data":    req,
	})
}
func (fh fsmHandler) Get(eCtx echo.Context) error {
	var key = strings.TrimSpace(eCtx.Param("key"))
	if key == "" {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": "key is empty",
		})
	}

	var keyByte = []byte(key)

	txn := fh.db.NewTransaction(false)
	defer func() {
		_ = txn.Commit()
	}()

	item, err := txn.Get(keyByte)
	if err != nil {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("error getting key %s from storage: %s", key, err.Error()),
		})
	}

	var value = make([]byte, 0)
	err = item.Value(func(val []byte) error {
		value = append(value, val...)
		return nil
	})

	if err != nil {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("error appending byte value of key %s from storage: %s", key, err.Error()),
		})
	}

	var data interface{}
	if value != nil && len(value) > 0 {
		err = json.Unmarshal(value, &data)
	}

	if err != nil {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("error unmarshal data: %s", err.Error()),
		})
	}

	return eCtx.JSON(http.StatusOK, map[string]interface{}{
		"message": "data fetched successfully",
		"data": map[string]interface{}{
			"key":   key,
			"value": data,
		},
	})
}
func (fh fsmHandler) Delete(eCtx echo.Context) error {
	var key = strings.TrimSpace(eCtx.Param("key"))
	if key == "" {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": "key is empty",
		})
	}

	if fh.raft.State() != raft.Leader {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": "not the leader",
		})
	}

	payload := fsm.CommandPayload{
		Operation: "DELETE",
		Key:       key,
		Value:     nil,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("error preparing remove data payload: %s", err.Error()),
		})
	}

	applyFuture := fh.raft.Apply(data, 500*time.Millisecond)
	if err := applyFuture.Error(); err != nil {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("error removing data in raft cluster: %s", err.Error()),
		})
	}

	_, ok := applyFuture.Response().(*fsm.ApplyResponse)
	if !ok {
		return eCtx.JSON(http.StatusUnprocessableEntity, map[string]interface{}{
			"error": fmt.Sprintf("error response is not match apply response"),
		})
	}

	return eCtx.JSON(http.StatusOK, map[string]interface{}{
		"message": "data removed successfully",
		"data": map[string]interface{}{
			"key":   key,
			"value": nil,
		},
	})
}

type httpServer struct {
	listenAddress string
	raft          *raft.Raft
	echo          *echo.Echo
}

func NewHTTPServer(listenAddr string, r *raft.Raft, db *badger.DB) *httpServer {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Pre(middleware.RemoveTrailingSlash())
	e.GET("/debug/pprof/*", echo.WrapHandler(http.DefaultServeMux))

	raftHandler := NewRaftHandler(r)
	e.POST("/raft/join", raftHandler.Join)
	e.POST("/raft/remove", raftHandler.Remove)
	e.GET("/raft/stats", raftHandler.Stats)

	fsmHandler := NewFSMHandler(r, db)
	e.POST("/db", fsmHandler.Set)
	e.GET("/db/:key", fsmHandler.Get)
	e.DELETE("/db/:key", fsmHandler.Delete)

	return &httpServer{
		listenAddress: listenAddr,
		echo:          e,
		raft:          r,
	}
}
func (s httpServer) Start() error {
	return s.echo.StartServer(&http.Server{
		Addr:         s.listenAddress,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	})
}
