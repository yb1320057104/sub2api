package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type asyncImageMemoryStore struct {
	mu    sync.RWMutex
	tasks map[string]*service.ImageTaskRecord
}

func (s *asyncImageMemoryStore) Save(_ context.Context, task *service.ImageTaskRecord, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *task
	copy.Result = append(json.RawMessage(nil), task.Result...)
	copy.Error = append(json.RawMessage(nil), task.Error...)
	s.tasks[task.ID] = &copy
	return nil
}

func (s *asyncImageMemoryStore) Get(_ context.Context, id string) (*service.ImageTaskRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task := s.tasks[id]
	if task == nil {
		return nil, service.ErrImageTaskNotFound
	}
	copy := *task
	copy.Result = append(json.RawMessage(nil), task.Result...)
	copy.Error = append(json.RawMessage(nil), task.Error...)
	return &copy, nil
}

func TestPKHiddenBillingAsyncImageHandlerSubmitAndPoll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	release := make(chan struct{})
	h := &AsyncImageHandler{tasks: tasks}
	h.execute = func(_ string, c *gin.Context) {
		<-release
		setOpsSelectedBillingAccount(c, &service.Account{ID: 81, Platform: service.PlatformOpenAI, Extra: map[string]any{service.AccountExtraHiddenRateMultiplier: 2}})
		c.JSON(http.StatusOK, gin.H{"created": 123, "data": []gin.H{{"url": "https://example.test/image.png"}}, "usage": gin.H{"input_tokens": 100, "output_tokens": 200, "total_tokens": 300}})
	}

	router := gin.New()
	router.Use(service.HiddenUsageOutputMiddleware())
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID:      9,
			UserID:  7,
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true},
		})
		if strings.HasSuffix(c.Request.URL.Path, "/async") {
			// The copied worker context must replace, not share, its parent's
			// output writer and earlier account binding.
			setOpsSelectedBillingAccount(c, &service.Account{ID: 80, Platform: service.PlatformOpenAI, Extra: map[string]any{service.AccountExtraHiddenRateMultiplier: 5}})
		}
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)
	router.GET("/v1/images/tasks/:task_id", h.Get)

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"cat"}`)).WithContext(requestCtx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "3", w.Header().Get("Retry-After"))

	var accepted struct {
		TaskID  string `json:"task_id"`
		Status  string `json:"status"`
		PollURL string `json:"poll_url"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &accepted))
	require.Equal(t, service.ImageTaskStatusProcessing, accepted.Status)
	require.Equal(t, "/v1/images/tasks/"+accepted.TaskID, accepted.PollURL)
	require.Equal(t, accepted.PollURL, w.Header().Get("Location"))

	// The detached background request must survive completion of/cancellation
	// from the short submission request.
	cancelRequest()
	close(release)
	require.Eventually(t, func() bool {
		got, err := tasks.Get(context.Background(), service.ImageTaskOwner{UserID: 7, APIKeyID: 9}, accepted.TaskID)
		return err == nil && got.Status == service.ImageTaskStatusCompleted
	}, time.Second, 10*time.Millisecond)

	pollReq := httptest.NewRequest(http.MethodGet, accepted.PollURL, nil)
	pollWriter := httptest.NewRecorder()
	router.ServeHTTP(pollWriter, pollReq)
	require.Equal(t, http.StatusOK, pollWriter.Code)
	require.Equal(t, "no-store", pollWriter.Header().Get("Cache-Control"))
	require.Empty(t, pollWriter.Header().Get("Retry-After"))
	require.Contains(t, pollWriter.Body.String(), "https://example.test/image.png")
	var completed struct {
		Result struct {
			Usage map[string]int `json:"usage"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(pollWriter.Body.Bytes(), &completed))
	require.Equal(t, map[string]int{"input_tokens": 200, "output_tokens": 400, "total_tokens": 600}, completed.Result.Usage)
	require.NotContains(t, pollWriter.Body.String(), "multiplier")
}

func TestPKHiddenBillingAsyncImageFailureAfterKeepalive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		overflow   bool
		wantStatus string
		wantHTTP   int
	}{
		{"overflow fails despite committed HTTP 200", true, service.ImageTaskStatusFailed, http.StatusBadGateway},
		{"unrelated Gin diagnostic does not fail successful output", false, service.ImageTaskStatusCompleted, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
			tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
			owner := service.ImageTaskOwner{UserID: 7, APIKeyID: 9}
			task, err := tasks.Create(context.Background(), owner)
			require.NoError(t, err)
			h := &AsyncImageHandler{tasks: tasks}
			h.execute = func(_ string, c *gin.Context) {
				setOpsSelectedBillingAccount(c, &service.Account{ID: 81, Platform: service.PlatformOpenAI, Extra: map[string]any{service.AccountExtraHiddenRateMultiplier: 2}})
				c.Header("Content-Type", "application/json")
				_, err := c.Writer.WriteString(" \n")
				require.NoError(t, err)
				c.Writer.Flush()
				require.True(t, c.Writer.Written())
				require.Equal(t, http.StatusOK, c.Writer.Status())
				if tc.overflow {
					_, err = c.Writer.WriteString(`{"usage":{"input_tokens":9223372036854775807},"data":[{"url":"https://example.test/image.png"}]}`)
					require.Error(t, err)
					return
				}
				_ = c.Error(errors.New("independent internal diagnostic"))
				c.JSON(http.StatusOK, gin.H{"data": []gin.H{{"url": "https://example.test/image.png"}}, "usage": gin.H{"input_tokens": 2, "output_tokens": 3, "total_tokens": 5}})
			}
			recorder := httptest.NewRecorder()
			taskCtx, _ := gin.CreateTestContext(recorder)
			executionCtx, cancel := context.WithCancel(context.Background())
			taskCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil).WithContext(executionCtx)
			h.run(task.ID, service.PlatformOpenAI, taskCtx, recorder, cancel)
			require.Equal(t, http.StatusOK, recorder.Code, "the keepalive's transport status cannot be replaced")
			if tc.overflow {
				require.Error(t, service.HiddenUsageOutputError(taskCtx))
			} else {
				require.NoError(t, service.HiddenUsageOutputError(taskCtx))
			}
			require.NoError(t, service.HiddenUsageOutputError(taskCtx.Copy()), "a copied context does not own the parent's failed writer")
			stored, err := tasks.Get(context.Background(), owner, task.ID)
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus, stored.Status)
			require.Equal(t, tc.wantHTTP, stored.HTTPStatus)
			if tc.overflow {
				require.Empty(t, stored.Result, "a usage error body must not be stored as a completed image result")
				require.JSONEq(t, `{"type":"upstream_error","message":"upstream response usage could not be processed"}`, string(stored.Error))
			}

			router := gin.New()
			router.Use(service.HiddenUsageOutputMiddleware(), func(c *gin.Context) {
				c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: owner.APIKeyID, UserID: owner.UserID})
				c.Next()
			})
			router.GET("/v1/images/tasks/:task_id", h.Get)
			pollWriter := httptest.NewRecorder()
			router.ServeHTTP(pollWriter, httptest.NewRequest(http.MethodGet, "/v1/images/tasks/"+task.ID, nil))
			require.Equal(t, http.StatusOK, pollWriter.Code)
			var polled service.ImageTask
			require.NoError(t, json.Unmarshal(pollWriter.Body.Bytes(), &polled))
			require.Equal(t, tc.wantStatus, polled.Status)
			require.Equal(t, tc.wantHTTP, polled.HTTPStatus)
			if tc.overflow {
				require.Empty(t, polled.Result)
				require.JSONEq(t, string(stored.Error), string(polled.Error))
			}
			require.NotContains(t, pollWriter.Body.String(), "9223372036854775807")
			require.NotContains(t, pollWriter.Body.String(), "multiplier")
			require.NotContains(t, pollWriter.Body.String(), "independent internal diagnostic")
		})
	}
}

// When object storage is not configured the feature is fully disabled: the
// endpoints must return 404 without creating a task or writing to Redis.
func TestAsyncImageHandlerDisabledReturns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithOptions(store, time.Hour, time.Minute) // enabled == false
	h := &AsyncImageHandler{tasks: tasks}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID:      9,
			UserID:  7,
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true},
		})
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)
	router.GET("/v1/images/tasks/:task_id", h.Get)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"cat"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "not enabled")

	pollReq := httptest.NewRequest(http.MethodGet, "/v1/images/tasks/imgtask_missing", nil)
	pollWriter := httptest.NewRecorder()
	router.ServeHTTP(pollWriter, pollReq)
	require.Equal(t, http.StatusNotFound, pollWriter.Code)

	// No task was created / persisted.
	require.Empty(t, store.tasks)
}
