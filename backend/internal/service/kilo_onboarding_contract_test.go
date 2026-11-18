package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestKiloOnboardingRowsRetainSourceAndProxyAssociation(t *testing.T) {
	input := KiloOnboardingBatchInput{RequestID: "61a9de91-c725-43fd-a3ba-609261d53d08", GroupIDs: []int64{9, 2}, Routing: KiloRoutingOptions{ProxyGroupID: 3, ProxiesPerAccount: 5, ProxyConcurrency: 20, AccountConcurrency: 100, Priority: 1}, Items: []KiloOnboardingItem{{SourceAccountID: 20, Name: "second", LoginProxyID: 50, VerifiedModelIDs: []string{"openai/gpt-5.1-codex", "openai/gpt-5.1-codex"}}, {SourceAccountID: 10, Name: "first", LoginProxyID: 40, VerifiedModelIDs: []string{"openai/gpt-5.1-codex"}}}}
	if err := normalizeKiloOnboarding(&input); err != nil {
		t.Fatal(err)
	}
	if input.Items[0].SourceAccountID != 20 || input.Items[0].LoginProxyID != 50 || input.Items[1].SourceAccountID != 10 || input.Items[1].LoginProxyID != 40 {
		t.Fatalf("normalization changed row association: %+v", input.Items)
	}
	if !reflect.DeepEqual(input.GroupIDs, []int64{2, 9}) || len(input.Items[0].VerifiedModelIDs) != 1 {
		t.Fatalf("normalization not canonical: %+v", input)
	}
	input.Items[0].VerifiedModelIDs = nil
	if err := normalizeKiloOnboarding(&input); err != nil {
		t.Fatalf("automatic authenticated catalog selection rejected: %v", err)
	}
	for _, bad := range [][]string{{"*"}, {"openai/*"}, {"anthropic/claude"}} {
		input.Items[0].VerifiedModelIDs = bad
		if err := normalizeKiloOnboarding(&input); err == nil {
			t.Fatalf("accepted unbounded model scope %v", bad)
		}
	}
}

func TestKiloOnboardingWorkspaceModeKeepsKiloOrganizationSeparate(t *testing.T) {
	base := KiloOnboardingBatchInput{RequestID: "61a9de91-c725-43fd-a3ba-609261d53d08", GroupIDs: []int64{2}, Routing: KiloRoutingOptions{ProxyGroupID: 3, ProxiesPerAccount: 5, ProxyConcurrency: 20, AccountConcurrency: 100}, Items: []KiloOnboardingItem{{SourceAccountID: 20, Name: "fixture", LoginProxyID: 50, OrganizationID: "ab730230-a046-4f05-b5b5-34fe345be176"}}}
	for _, mode := range []string{"", "auto", "personal", "business", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			input := base
			input.Items = append([]KiloOnboardingItem(nil), base.Items...)
			input.Items[0].WorkspaceMode = mode
			err := normalizeKiloOnboarding(&input)
			if mode == "invalid" {
				if err == nil {
					t.Fatal("invalid workspace mode accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if input.Items[0].OrganizationID != "ab730230-a046-4f05-b5b5-34fe345be176" {
				t.Fatal("GPT mode changed Kilo organization")
			}
			if kiloEffectiveWorkspaceMode(input.Items[0].WorkspaceMode) != kiloEffectiveWorkspaceMode(mode) {
				t.Fatal("workspace mode lost")
			}
		})
	}
}

func TestKiloExecutorUsesAuthenticatedLocalProtocolWithoutRedirect(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer executor-test-token-1234567890-abcd" {
			t.Error("missing executor bearer")
		}
		if r.URL.Path == "/v1/health" {
			_ = json.NewEncoder(w).Encode(KiloExecutorHealth{OK: true, ProtocolVersion: 1, InstanceID: "instance-a", AvailableCapacity: 1})
			return
		}
		if r.URL.Path == "/v1/tasks" {
			var in KiloExecutorTaskInput
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.JobID != 71 || in.Revision != 3 || in.Phase != "login" {
				t.Error("lost execution fence")
			}
			if in.ExpectedIdentity.SourceEmail != "source@example.test" || in.ExpectedIdentity.SourceWorkspaceID != "source-oauth-workspace" || in.ExpectedIdentity.SourcePlanType != "business" || in.OrganizationID != "distinct-kilo-organization" {
				t.Error("lost source workspace, plan or separate Kilo organization")
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(KiloExecutorTask{TaskID: "opaque", InstanceID: "instance-a", JobID: in.JobID, Revision: in.Revision, Phase: in.Phase, Status: "queued", ConnectionWorkspaceID: in.ExpectedIdentity.SourceWorkspaceID})
			return
		}
		http.Redirect(w, r, "https://invalid.example.test/steal", http.StatusFound)
	}))
	defer server.Close()
	t.Setenv("KILO_EXECUTOR_URL", server.URL)
	t.Setenv("KILO_EXECUTOR_TOKEN", "executor-test-token-1234567890-abcd")
	client := newKiloExecutorFromEnv()
	if client == nil {
		t.Fatal("configured local executor missing")
	}
	if health, err := client.Health(context.Background()); err != nil || health.InstanceID != "instance-a" {
		t.Fatalf("health: %+v %v", health, err)
	}
	input := KiloExecutorTaskInput{ProtocolVersion: 1, JobID: 71, Revision: 3, OperationID: "fenced-op", Phase: "login", DeadlineAt: time.Now().Add(time.Minute), OrganizationID: "distinct-kilo-organization"}
	input.ExpectedIdentity.SourceEmail = "source@example.test"
	input.ExpectedIdentity.SourceWorkspaceID = "source-oauth-workspace"
	input.ExpectedIdentity.SourcePlanType = "business"
	result, err := client.Submit(context.Background(), input)
	if err != nil || result.Revision != 3 || result.ConnectionWorkspaceID != input.ExpectedIdentity.SourceWorkspaceID {
		t.Fatalf("submit: %+v %v", result, err)
	}
	if _, err = client.Task(context.Background(), "redirect"); err == nil {
		t.Fatal("executor redirect must fail without forwarding bearer")
	}
	if calls != 3 {
		t.Fatalf("unexpected protocol calls: %d", calls)
	}
}
