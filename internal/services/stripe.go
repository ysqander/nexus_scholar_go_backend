package services

import (
	"fmt"
	"os"

	"github.com/stripe/stripe-go/v79"
	"github.com/stripe/stripe-go/v79/checkout/session"
	"github.com/stripe/stripe-go/v79/webhook"
)

type StripeService struct {
	publicKey string
	secretKey string
}

func NewStripeService(publicKey, secretKey string) *StripeService {
	stripe.Key = secretKey
	return &StripeService{
		publicKey: publicKey,
		secretKey: secretKey,
	}
}

func (s *StripeService) CreateCheckoutSession(userID string, priceID string, tokenHours float64, priceTier string) (*stripe.CheckoutSession, error) {
	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		return nil, fmt.Errorf("FRONTEND_URL is not set")
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
		SuccessURL:        stripe.String(fmt.Sprintf("%s/success?session_id={CHECKOUT_SESSION_ID}", frontendURL)),
		CancelURL:         stripe.String(fmt.Sprintf("%s/cancel", frontendURL)),
		ClientReferenceID: stripe.String(userID),
		Metadata: map[string]string{
			"token_hours": fmt.Sprintf("%.2f", tokenHours),
			"price_tier":  priceTier,
		},
	}
	return session.New(params)
}

func (s *StripeService) HandleWebhook(payload []byte, signatureHeader string) (stripe.Event, error) {
	endpointSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	if endpointSecret == "" {
		return stripe.Event{}, fmt.Errorf("STRIPE_WEBHOOK_SECRET is not set")
	}
	return webhook.ConstructEvent(payload, signatureHeader, endpointSecret)
}

func (s *StripeService) HandleWebhook_clitest(payload []byte, signatureHeader string) (stripe.Event, error) {
	endpointSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	if endpointSecret == "" {
		return stripe.Event{}, fmt.Errorf("STRIPE_WEBHOOK_SECRET is not set")
	}
	return webhook.ConstructEventWithOptions(payload, signatureHeader, endpointSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
}
