package services

import (
	"fmt"
	"os"

	"github.com/rs/zerolog"
	"github.com/stripe/stripe-go/v79"
	"github.com/stripe/stripe-go/v79/checkout/session"
	"github.com/stripe/stripe-go/v79/webhook"
)

type StripeService struct {
	publicKey string
	secretKey string
	logger    zerolog.Logger
}

func NewStripeService(publicKey, secretKey string, logger zerolog.Logger) *StripeService {
	stripe.Key = secretKey
	return &StripeService{
		publicKey: publicKey,
		secretKey: secretKey,
		logger:    logger,
	}
}

func (s *StripeService) CreateCheckoutSession(userID string, priceID string, tokenHours float64, priceTier string) (*stripe.CheckoutSession, error) {
	s.logger.Info().Msgf("Creating checkout session for user ID: %s, price ID: %s, token hours: %.2f, price tier: %s", userID, priceID, tokenHours, priceTier)

	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		s.logger.Error().Msg("FRONTEND_URL is not set")
		return nil, fmt.Errorf("FRONTEND_URL is not set")
	}

	webhookEndpoint := os.Getenv("STRIPE_WEBHOOK_ENDPOINT")
	if webhookEndpoint == "" {
		s.logger.Error().Msg("STRIPE_WEBHOOK_ENDPOINT is not set")
		return nil, fmt.Errorf("STRIPE_WEBHOOK_ENDPOINT is not set")
	}

	params := &stripe.CheckoutSessionParams{
		PaymentMethodTypes: stripe.StringSlice([]string{
			"card",
		}),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(priceID),
				Quantity: stripe.Int64(int64(tokenHours)),
			},
		},
		Mode:              stripe.String(string(stripe.CheckoutSessionModePayment)),
		SuccessURL:        stripe.String(fmt.Sprintf("%s/stripesuccess?session_id={CHECKOUT_SESSION_ID}", frontendURL)),
		CancelURL:         stripe.String(fmt.Sprintf("%s/cancel", frontendURL)),
		ClientReferenceID: stripe.String(userID),
		Metadata: map[string]string{
			"token_hours": fmt.Sprintf("%.2f", tokenHours),
			"price_tier":  priceTier,
		},
		PaymentIntentData: &stripe.CheckoutSessionPaymentIntentDataParams{
			SetupFutureUsage: stripe.String(string(stripe.PaymentIntentSetupFutureUsageOffSession)),
		},
	}
	params.AddExpand("payment_intent")

	session, err := session.New(params)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to create checkout session")
		return nil, err
	}

	s.logger.Info().Msgf("Checkout session created successfully. Session ID: %s", session.ID)
	return session, nil
}

func (s *StripeService) HandleWebhook(payload []byte, signatureHeader string) (stripe.Event, error) {
	s.logger.Info().Msg("Handling Stripe webhook")

	endpointSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	if endpointSecret == "" {
		s.logger.Error().Msg("STRIPE_WEBHOOK_SECRET is not set")
		return stripe.Event{}, fmt.Errorf("STRIPE_WEBHOOK_SECRET is not set")
	}

	event, err := webhook.ConstructEvent(payload, signatureHeader, endpointSecret)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to construct webhook event")
		return stripe.Event{}, err
	}

	s.logger.Info().Msgf("Webhook event constructed successfully. Event type: %s", event.Type)
	return event, nil
}

func (s *StripeService) HandleWebhook_clitest(payload []byte, signatureHeader string) (stripe.Event, error) {
	s.logger.Info().Msg("Handling Stripe webhook (CLI test)")

	endpointSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	if endpointSecret == "" {
		s.logger.Error().Msg("STRIPE_WEBHOOK_SECRET is not set")
		return stripe.Event{}, fmt.Errorf("STRIPE_WEBHOOK_SECRET is not set")
	}

	event, err := webhook.ConstructEventWithOptions(payload, signatureHeader, endpointSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to construct webhook event (CLI test)")
		return stripe.Event{}, err
	}

	s.logger.Info().Msgf("Webhook event constructed successfully (CLI test). Event type: %s", event.Type)
	return event, nil
}
