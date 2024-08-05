package services

import (
	"fmt"

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

func (s *StripeService) CreateCheckoutSession(userID string, tokenHours float64, priceTier string, amount int64) (*stripe.CheckoutSession, error) {
	params := &stripe.CheckoutSessionParams{
		PaymentMethodTypes: stripe.StringSlice([]string{
			"card",
		}),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
					Currency: stripe.String("usd"),
					ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
						Name: stripe.String(fmt.Sprintf("%.2f Million Token-Hours", tokenHours)),
					},
					UnitAmount: &amount,
				},
				Quantity: stripe.Int64(1),
			},
		},
		Mode:              stripe.String(string(stripe.CheckoutSessionModePayment)),
		SuccessURL:        stripe.String("https://yourapp.com/success?session_id={CHECKOUT_SESSION_ID}"),
		CancelURL:         stripe.String("https://yourapp.com/cancel"),
		ClientReferenceID: stripe.String(userID),
		Metadata: map[string]string{
			"token_hours": fmt.Sprintf("%.2f", tokenHours),
			"price_tier":  priceTier,
		},
	}

	return session.New(params)
}

func (s *StripeService) HandleWebhook(payload []byte, signatureHeader string) (stripe.Event, error) {
	endpointSecret := "whsec_your_webhook_signing_secret" // Replace with your actual webhook signing secret
	return webhook.ConstructEvent(payload, signatureHeader, endpointSecret)
}
