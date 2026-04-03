package fail_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/grafana/gcx/cmd/gcx/fail"
	"github.com/grafana/gcx/internal/datasources"
	"github.com/grafana/gcx/internal/grafana"
	"github.com/grafana/gcx/internal/queryerror"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8sapi "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestErrorToDetailedError_ContextCanceled(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantExitCode int
	}{
		{
			name:         "bare context.Canceled returns ExitCancelled",
			err:          context.Canceled,
			wantExitCode: fail.ExitCancelled,
		},
		{
			name:         "wrapped context.Canceled returns ExitCancelled",
			err:          fmt.Errorf("operation failed: %w", context.Canceled),
			wantExitCode: fail.ExitCancelled,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fail.ErrorToDetailedError(tc.err)

			require.NotNil(t, got)
			require.NotNil(t, got.ExitCode)
			assert.Equal(t, tc.wantExitCode, *got.ExitCode)
		})
	}
}

func TestErrorToDetailedError_NonCanceledError(t *testing.T) {
	got := fail.ErrorToDetailedError(errors.New("some other error"))

	require.NotNil(t, got)
	assert.Nil(t, got.ExitCode, "non-canceled errors should have nil ExitCode")
	assert.Equal(t, "Some other error", got.Summary)
	assert.Empty(t, got.Details)
	assert.Nil(t, got.Parent)
}

func TestErrorToDetailedError_WrappedErrorUsesOuterSummary(t *testing.T) {
	got := fail.ErrorToDetailedError(fmt.Errorf("failed to create client: %w", errors.New("dial tcp 127.0.0.1: connect: connection refused")))

	require.NotNil(t, got)
	assert.Equal(t, "Failed to create client", got.Summary)
	require.Error(t, got.Parent)
	assert.Equal(t, "dial tcp 127.0.0.1: connect: connection refused", got.Parent.Error())
}

func TestErrorToDetailedError_ColonSeparatedMessageSplitsSummaryAndDetails(t *testing.T) {
	got := fail.ErrorToDetailedError(errors.New("datasource UID is required: use -d flag or set datasources.loki in config"))

	require.NotNil(t, got)
	assert.Equal(t, "Datasource UID is required", got.Summary)
	assert.Equal(t, "use -d flag or set datasources.loki in config", got.Details)
}

func TestErrorToDetailedError_AuthExitCode(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantExitCode int
	}{
		{
			name: "401 Unauthorized returns ExitAuthFailure",
			err: &k8sapi.StatusError{
				ErrStatus: metav1.Status{
					Status:  metav1.StatusFailure,
					Code:    401,
					Reason:  metav1.StatusReasonUnauthorized,
					Message: "Unauthorized",
				},
			},
			wantExitCode: fail.ExitAuthFailure,
		},
		{
			name: "403 Forbidden returns ExitAuthFailure",
			err: &k8sapi.StatusError{
				ErrStatus: metav1.Status{
					Status:  metav1.StatusFailure,
					Code:    403,
					Reason:  metav1.StatusReasonForbidden,
					Message: "Forbidden",
				},
			},
			wantExitCode: fail.ExitAuthFailure,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fail.ErrorToDetailedError(tc.err)

			require.NotNil(t, got)
			require.NotNil(t, got.ExitCode, "ExitCode should be set for auth errors")
			assert.Equal(t, tc.wantExitCode, *got.ExitCode)
		})
	}
}

func TestErrorToDetailedError_VersionIncompatible(t *testing.T) {
	v, err := semver.NewVersion("11.5.0")
	require.NoError(t, err)

	got := fail.ErrorToDetailedError(&grafana.VersionIncompatibleError{Version: v})

	require.NotNil(t, got)
	require.NotNil(t, got.ExitCode, "ExitCode should be set for version incompatibility")
	assert.Equal(t, fail.ExitVersionIncompatible, *got.ExitCode)
}

func TestErrorToDetailedError_QueryParseError(t *testing.T) {
	err := fmt.Errorf("query failed: %w", queryerror.New(
		"loki",
		"query",
		400,
		"parse error at line 1, col 12: syntax error: unexpected IDENTIFIER, expecting STRING",
		"downstream",
	))

	got := fail.ErrorToDetailedError(err)

	require.NotNil(t, got)
	assert.Equal(t, "Invalid LogQL query", got.Summary)
	assert.Equal(t, "parse error at line 1, col 12: syntax error: unexpected IDENTIFIER, expecting STRING", got.Details)
	require.Len(t, got.Suggestions, 2)
	assert.Equal(t, `Try a quoted selector value, e.g. gcx logs query '{namespace="prod"}'`, got.Suggestions[0])
	assert.Equal(t, "Run 'gcx logs query --help' for usage and examples", got.Suggestions[1])
	assert.Nil(t, got.ExitCode)
}

func TestErrorToDetailedError_QueryAuthFailure(t *testing.T) {
	got := fail.ErrorToDetailedError(queryerror.New("prometheus", "query", 401, "unauthorized", ""))

	require.NotNil(t, got)
	assert.Equal(t, "Authentication failed querying Prometheus", got.Summary)
	require.NotNil(t, got.ExitCode)
	assert.Equal(t, fail.ExitAuthFailure, *got.ExitCode)
	assert.Equal(t, []string{
		"Review your Grafana credentials: gcx config view",
		"Re-authenticate if needed: gcx auth login",
	}, got.Suggestions)
}

func TestErrorToDetailedError_DatasourceNotFound(t *testing.T) {
	got := fail.ErrorToDetailedError(fmt.Errorf("failed to get datasource: %w", &datasources.APIError{
		Operation:  "get datasource",
		Identifier: "missing",
		StatusCode: 404,
		Message:    "Datasource not found",
	}))

	require.NotNil(t, got)
	assert.Equal(t, `Datasource "missing" not found`, got.Summary)
	assert.Equal(t, "Datasource not found", got.Details)
	assert.Equal(t, []string{"List available datasources: gcx datasources list"}, got.Suggestions)
}

func TestErrorToDetailedError_WrappedDatasourceErrorPreservesOuterGuidance(t *testing.T) {
	err := fmt.Errorf(
		"SM metrics datasource %q not found in Grafana: %w; use --datasource-uid or set default-prometheus-datasource in config",
		"sm-prom",
		&datasources.APIError{
			Operation:  "get datasource",
			Identifier: "sm-prom",
			StatusCode: 404,
			Message:    "Datasource not found",
		},
	)

	got := fail.ErrorToDetailedError(err)

	require.NotNil(t, got)
	assert.Equal(t, `Datasource "sm-prom" not found`, got.Summary)
	assert.Contains(t, got.Details, `SM metrics datasource "sm-prom" not found in Grafana`)
	assert.Contains(t, got.Details, "use --datasource-uid or set default-prometheus-datasource in config")
	assert.Contains(t, got.Details, "Datasource not found")
	assert.Equal(t, []string{"List available datasources: gcx datasources list"}, got.Suggestions)
}

func TestErrorToDetailedError_QueryNotFoundUsesResourceSummary(t *testing.T) {
	got := fail.ErrorToDetailedError(queryerror.New("tempo", "get trace", 404, "trace not found", ""))

	require.NotNil(t, got)
	assert.Equal(t, "Trace not found", got.Summary)
	assert.Equal(t, "trace not found", got.Details)
}

func TestErrorToDetailedError_GenericServiceAPIAuthFailure(t *testing.T) {
	got := fail.ErrorToDetailedError(fakeServiceAPIError{statusCode: 401, service: "Adaptive Logs", message: "invalid API token"})

	require.NotNil(t, got)
	assert.Equal(t, "Authentication failed querying Adaptive Logs", got.Summary)
	assert.Equal(t, "invalid API token", got.Details)
	require.NotNil(t, got.ExitCode)
	assert.Equal(t, fail.ExitAuthFailure, *got.ExitCode)
}

func TestErrorToDetailedError_WrappedServiceAPIErrorPreservesOuterContext(t *testing.T) {
	err := fmt.Errorf("kg: get rule %q: %w", "prod-errors", fakeServiceAPIError{
		statusCode: 404,
		service:    "Knowledge Graph",
		message:    "rule not found",
	})

	got := fail.ErrorToDetailedError(err)

	require.NotNil(t, got)
	assert.Equal(t, "Knowledge Graph API resource not found", got.Summary)
	assert.Contains(t, got.Details, `kg: get rule "prod-errors"`)
	assert.Contains(t, got.Details, "rule not found")
}

func TestErrorToDetailedError_ConverterOrdering(t *testing.T) {
	// A context.Canceled wrapping a 401 error should be classified as
	// cancelled (exit 5), not as auth failure (exit 3), because the
	// cancellation converter runs first in the chain.
	unauthorizedErr := &k8sapi.StatusError{
		ErrStatus: metav1.Status{
			Status:  metav1.StatusFailure,
			Code:    401,
			Reason:  metav1.StatusReasonUnauthorized,
			Message: "Unauthorized",
		},
	}
	wrappedErr := fmt.Errorf("request failed: %w: %w", context.Canceled, unauthorizedErr)

	got := fail.ErrorToDetailedError(wrappedErr)

	require.NotNil(t, got)
	require.NotNil(t, got.ExitCode, "ExitCode should be set")
	assert.Equal(t, fail.ExitCancelled, *got.ExitCode, "context.Canceled should take precedence over auth errors")
}

func TestErrorToDetailedError_UsageErrorIncludesExpectedSyntax(t *testing.T) {
	rootCmd := &cobra.Command{Use: "gcx"}
	logsCmd := &cobra.Command{Use: "logs"}
	queryCmd := &cobra.Command{Use: "query [DATASOURCE_UID] EXPR"}
	queryCmd.Flags().Bool("json", false, "")

	rootCmd.AddCommand(logsCmd)
	logsCmd.AddCommand(queryCmd)

	got := fail.ErrorToDetailedError(fail.NewCommandUsageError(queryCmd, "EXPR is required", nil))

	require.NotNil(t, got)
	assert.Equal(t, "Invalid command usage", got.Summary)
	assert.Contains(t, got.Details, "EXPR is required")
	assert.Contains(t, got.Details, "Expected:")
	assert.Contains(t, got.Details, "gcx logs query [DATASOURCE_UID] EXPR [flags]")
	require.Len(t, got.Suggestions, 1)
	assert.Equal(t, "Run 'gcx logs query --help' for full usage and examples", got.Suggestions[0])
}

type fakeServiceAPIError struct {
	statusCode int
	service    string
	message    string
}

func (e fakeServiceAPIError) Error() string {
	return e.message
}

func (e fakeServiceAPIError) HTTPStatusCode() int {
	return e.statusCode
}

func (e fakeServiceAPIError) APIServiceName() string {
	return e.service
}

func (e fakeServiceAPIError) APIUserMessage() string {
	return e.message
}
