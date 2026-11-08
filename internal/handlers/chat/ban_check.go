package chat

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/providers"
)

// BanCheckVerdict is what one ban check concluded about one account.
type BanCheckVerdict struct {
	// Banned is true when the upstream answered with an account-suspension
	// refusal for this credential.
	Banned bool
	// Lifted is true when the request succeeded: the appeal was granted, or the
	// account was never banned.
	Lifted bool
	// Status is the upstream HTTP status, 0 when the request never reached it.
	Status int
	// Message is the upstream's own wording, which is what an operator needs to
	// read — Google names the appeal in it.
	Message string
}

// CheckConnectionBan asks one named account whether its ban is still in force.
//
// It is the ordinary chat lane with the connection pinned, so the credential
// resolution, OAuth refresh, proxy-pool binding and provider executor are the
// ones real traffic uses. A token-only probe cannot answer this question: a
// banned Google account still holds a valid OAuth token and refreshes it
// happily, which is why "Test connection" reports success on an account that
// cannot serve a single request.
//
// The ban-check marker is what lets this reach a disabled row. A banned account
// is normally left switched off, so that state is precisely the one the question
// is about.
//
// One request, one account, no rotation: a verdict about this credential must
// not be answered by a different one.
func (h *ChatHandler) CheckConnectionBan(ctx context.Context, provider, model, connectionID string, body []byte) (BanCheckVerdict, error) {
	if h == nil {
		return BanCheckVerdict{}, fmt.Errorf("no chat handler")
	}
	if connectionID == "" {
		return BanCheckVerdict{}, fmt.Errorf("no connection id")
	}
	if model == "" {
		return BanCheckVerdict{}, fmt.Errorf("no model")
	}

	ctx = handlerutil.WithBanCheckContext(ctx)

	rec := &pingResponseWriter{}
	err := h.handleAccountFallback(ctx, rec, provider, model, connectionID, body, false, false, "/v1/chat/completions")
	if err == nil {
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		return BanCheckVerdict{Lifted: true, Status: status}, nil
	}

	// The upstream's own body is the evidence. A refusal that names an account
	// suspension is the ban still standing; anything else is a fault the
	// operator has to read, and reporting it as "lifted" would invite them to
	// switch the account back on.
	var ue *upstreamError
	if errors.As(err, &ue) {
		text := extractErrorText(ue.Body)
		if text == "" {
			text = err.Error()
		}
		return BanCheckVerdict{
			Banned:  providers.LooksLikeAccountSuspension(ue.StatusCode, text),
			Status:  ue.StatusCode,
			Message: text,
		}, nil
	}

	return BanCheckVerdict{Message: err.Error()}, nil
}
