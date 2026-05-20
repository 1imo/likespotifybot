// Has db stub injected in
// Pulls broadcasts and user ids from broadcast and users tables
// Pulls from broadcast_outgoing where sent_time is null
// Check that scheduled_at is in the past, if not skip
// For each broadcast, determine which broadcast type it is, message or quiz
// Uses requres use-cases to handle the broadcast
// Updates broadcast_outgoing with sent_time and telegram_message_id after a successful send

package handlebroadcast

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	handlemessage "likespotifybot/use-cases/handle-message"
	"likespotifybot/utils"
	"likespotifybot/utils/db"
)

const pendingOutgoingReadCacheTTL = 0
const broadcastRunSlowThreshold = 2 * time.Second

// SubscriptionStrainCardBuilder rebuilds plain strain HTML for subscriber fan-out (no encounter line, no collection button).
type SubscriptionStrainCardBuilder interface {
	BuildSubscriptionStrainCard(ctx context.Context, recipientTelegramID int64, strainCanonical string) (handlemessage.OutboundMessage, error)
}

type RootUseCase struct {
	store                  db.DB
	analytics              *utils.Analytics
	messageBroadcast       MessageBroadcaster
	quizBroadcaster        QuizBroadcaster
	strainSubscriptionCard SubscriptionStrainCardBuilder
	log                    *utils.Logger
}

func NewRootUseCase(
	store db.DB,
	analytics *utils.Analytics,
	messageBroadcaster MessageBroadcaster,
	quizBroadcaster QuizBroadcaster,
	strainSubscriptionCards SubscriptionStrainCardBuilder,
	log *utils.Logger,
) *RootUseCase {
	return &RootUseCase{
		store:                  store,
		analytics:              analytics,
		messageBroadcast:       messageBroadcaster,
		quizBroadcaster:        quizBroadcaster,
		strainSubscriptionCard: strainSubscriptionCards,
		log:                    log,
	}
}

func broadcastRunTimeout() time.Duration {
	const defaultSec = 120
	raw := strings.TrimSpace(os.Getenv("BROADCAST_RUN_TIMEOUT_SECONDS"))
	if raw == "" {
		return defaultSec * time.Second
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultSec * time.Second
	}
	return time.Duration(n) * time.Second
}

func (u *RootUseCase) RunOnce() error {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), broadcastRunTimeout())
	defer cancel()
	processed := 0
	sent := 0
	aborted := 0

	// One statement replaces hundreds of per-row subscription checks (avoids context deadline exceeded).
	if err := u.closeStrainOutgoingWithoutSubscription(ctx); err != nil {
		return err
	}

	rows, err := u.store.QueryContext(
		ctx,
		`SELECT bo.broadcast_id, bo.user_id, b.type, b.payload
		 FROM broadcast_outgoing bo
		 INNER JOIN broadcasts b ON b.id = bo.broadcast_id
		 WHERE bo.sent_time IS NULL
		   AND (bo.scheduled_at IS NULL OR bo.scheduled_at <= NOW())
		   AND GREATEST(COALESCE(bo.scheduled_at, '-infinity'::timestamptz), bo.created_at) >= NOW() - INTERVAL '12 hours'
		 ORDER BY bo.created_at DESC
		 LIMIT 500`,
		pendingOutgoingReadCacheTTL,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		processed++
		var broadcastID string
		var userID int64
		var kind string
		var payloadRaw []byte
		if err := rows.Scan(&broadcastID, &userID, &kind, &payloadRaw); err != nil {
			return err
		}

		var payload map[string]any
		_ = json.Unmarshal(payloadRaw, &payload)

		var tgMsg sql.NullInt64
		sentOK := false

		switch kind {
		case "message":
			canonical := strings.TrimSpace(fmt.Sprint(payload["strain_canonical"]))
			textFallback, _ := payload["text"].(string)
			textFallback = strings.TrimSpace(textFallback)
			outbound := handlemessage.OutboundMessage{}
			if canonical != "" && u.strainSubscriptionCard != nil {
				built, berr := u.strainSubscriptionCard.BuildSubscriptionStrainCard(ctx, userID, canonical)
				if berr != nil && u.log != nil {
					u.log.Warn("broadcast strain card rebuild failed broadcast_id=%s chat_id=%d strain=%q err=%v — falling back to payload text", broadcastID, userID, canonical, berr)
				} else {
					outbound = built
				}
			}
			if strings.TrimSpace(outbound.Text) == "" {
				outbound = handlemessage.OutboundMessage{Text: textFallback}
			}
			if strings.TrimSpace(outbound.Text) != "" && u.messageBroadcast != nil {
				mid, err := u.messageBroadcast.SendOutbound(userID, outbound)
				if err != nil {
					if u.log != nil {
						u.log.Error("broadcast message send failed broadcast_id=%s chat_id=%d: %v", broadcastID, userID, err)
					}
					if permanentTelegramDeliveryError(err) {
						if aborterr := u.abortUndeliverableOutgoing(ctx, broadcastID, userID); aborterr != nil {
							return aborterr
						}
						aborted++
						if u.log != nil {
							u.log.Warn("broadcast row closed (undeliverable); will not retry broadcast_id=%s chat_id=%d", broadcastID, userID)
						}
						_ = u.analytics.TrackEvent(ctx, utils.AnalyticsEvent{
							Name:     "broadcast",
							UserID:   userID,
							EntityID: broadcastID,
							Status:   "aborted",
							Meta: map[string]any{
								"type": kind, "broadcast_id": broadcastID, "reason": "telegram_permanent_error", "error": err.Error(),
							},
						})
					}
					continue
				}
				tgMsg = sql.NullInt64{Int64: mid, Valid: true}
				sentOK = true
			}
		case "quiz":
			if u.quizBroadcaster != nil {
				question, _ := payload["question"].(string)
				optionsAny, _ := payload["options"].([]any)
				options := make([]string, 0, len(optionsAny))
				for _, v := range optionsAny {
					s, ok := v.(string)
					if ok {
						options = append(options, s)
					}
				}
				correctIndex := 0
				if ci, ok := payload["correct_index"].(float64); ok {
					correctIndex = int(ci)
				}
				if question != "" && len(options) >= 2 {
					mid, err := u.quizBroadcaster.SendQuiz(userID, question, options, correctIndex)
					if err != nil {
						if u.log != nil {
							u.log.Error("broadcast quiz send failed broadcast_id=%s chat_id=%d: %v", broadcastID, userID, err)
						}
						if permanentTelegramDeliveryError(err) {
							if aborterr := u.abortUndeliverableOutgoing(ctx, broadcastID, userID); aborterr != nil {
								return aborterr
							}
							aborted++
							if u.log != nil {
								u.log.Warn("broadcast quiz row closed (undeliverable); will not retry broadcast_id=%s chat_id=%d", broadcastID, userID)
							}
						}
						continue
					}
					tgMsg = sql.NullInt64{Int64: mid, Valid: true}
					sentOK = true
				}
			}
		}

		if !sentOK {
			continue
		}
		sent++

		if _, err := u.store.ExecContext(
			ctx,
			`UPDATE broadcast_outgoing
			 SET sent_time = NOW(),
			     telegram_message_id = $3
			 WHERE broadcast_id = $1 AND user_id = $2 AND sent_time IS NULL`,
			broadcastID, userID, tgMsg,
		); err != nil {
			return err
		}

		meta := map[string]any{"type": kind, "broadcast_id": broadcastID}
		if tgMsg.Valid {
			meta["telegram_message_id"] = tgMsg.Int64
		}
		_ = u.analytics.TrackEvent(ctx, utils.AnalyticsEvent{
			Name:     "broadcast",
			UserID:   userID,
			EntityID: broadcastID,
			Status:   "sent",
			Meta:     meta,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if u.log != nil {
		durationMs := time.Since(started).Milliseconds()
		if time.Since(started) >= broadcastRunSlowThreshold {
			u.log.Warn("event=broadcast-run status=slow duration_ms=%d processed=%d sent=%d aborted=%d", durationMs, processed, sent, aborted)
		} else {
			u.log.Info("event=broadcast-run status=ok duration_ms=%d processed=%d sent=%d aborted=%d", durationMs, processed, sent, aborted)
		}
	}
	return nil
}

// closeStrainOutgoingWithoutSubscription marks pending strain fan-out rows as done when the chat
// is not subscribed (or disabled). Pending rows are otherwise retried every tick and can stall the run.
func (u *RootUseCase) closeStrainOutgoingWithoutSubscription(ctx context.Context) error {
	res, err := u.store.ExecContext(
		ctx,
		`UPDATE broadcast_outgoing AS bo
		 SET sent_time = NOW(),
		     telegram_message_id = NULL
		 FROM broadcasts AS b
		 WHERE bo.broadcast_id = b.id
		   AND bo.sent_time IS NULL
		   AND b.id ~ '^strain-'
		   AND NOT EXISTS (
		     SELECT 1 FROM subscriptions s
		     WHERE s.telegram_id = bo.user_id AND s.enabled = TRUE
		   )`,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if u.log != nil && n > 0 {
		u.log.Info("closed %d strain broadcast_outgoing rows (subscription disabled or missing)", n)
	}
	return nil
}

// abortUndeliverableOutgoing sets sent_time so the scheduler stops retrying the same row forever
// (e.g. bot blocked, group write forbidden). telegram_message_id stays NULL.
func (u *RootUseCase) abortUndeliverableOutgoing(ctx context.Context, broadcastID string, userID int64) error {
	_, err := u.store.ExecContext(
		ctx,
		`UPDATE broadcast_outgoing
		 SET sent_time = NOW(),
		     telegram_message_id = NULL
		 WHERE broadcast_id = $1 AND user_id = $2 AND sent_time IS NULL`,
		broadcastID, userID,
	)
	return err
}

func permanentTelegramDeliveryError(err error) bool {
	if err == nil {
		return false
	}
	var e tgbotapi.Error
	if errors.As(err, &e) {
		switch e.Code {
		case 403:
			return true
		case 400:
			m := strings.ToLower(e.Message)
			return strings.Contains(m, "chat not found") ||
				strings.Contains(m, "peer_id_invalid") ||
				strings.Contains(m, "not enough rights") ||
				strings.Contains(m, "have no rights") ||
				strings.Contains(m, "chat_write_forbidden") ||
				strings.Contains(m, "group chat was upgraded") ||
				strings.Contains(m, "channel_invalid") ||
				strings.Contains(m, "user is deactivated")
		default:
			return false
		}
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "forbidden") ||
		strings.Contains(low, "bot was blocked") ||
		strings.Contains(low, "kicked") ||
		strings.Contains(low, "chat_write_forbidden")
}
