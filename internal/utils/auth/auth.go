package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"nexus_scholar_go_backend/internal/services"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

func SetupRoutes(r *gin.Engine, userService *services.UserService) {
	auth := r.Group("/auth")
	{
		auth.GET("/user", AuthMiddleware(userService), getUser)
	}
}

func AuthMiddleware(userService *services.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		log := zerolog.Ctx(c.Request.Context())
		log.Debug().Msg("Auth middleware started")

		authHeader := c.GetHeader("Authorization")
		log.Debug().Msgf("Authorization header present: %v", authHeader != "")

		var token string

		// Extract token. Check if it's a WebSocket upgrade request first.
		if websocket.IsWebSocketUpgrade(c.Request) {
			// Extract token from query parameter for WebSocket connections
			token = c.Query("token")
		} else {
			// Extract token from Authorization header
			authHeader := c.GetHeader("Authorization")

			if authHeader == "" {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header is required"})
				c.Abort()
				return
			}

			bearerToken := strings.Split(authHeader, " ")
			if len(bearerToken) != 2 {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid authorization header"})
				c.Abort()
				return
			}
			token = bearerToken[1]
		}

		// Verify the token
		claims, err := verifyToken(token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			c.Abort()
			return
		}

		// Extract user info from claims
		auth0ID, _ := claims["sub"].(string)
		email, _ := claims["email"].(string)
		name, _ := claims["name"].(string)
		nickname, _ := claims["nickname"].(string)

		// Create or update user
		user, err := userService.CreateOrUpdateUser(c.Request.Context(), auth0ID, email, name, nickname)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process user information"})
			c.Abort()
			return
		}

		// Set the user in the context
		c.Set("user", user)
		c.Next()
	}
}

func getUser(c *gin.Context) {
	user, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in context"})
		return
	}
	c.JSON(http.StatusOK, user)
}

func verifyToken(tokenString string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		cert, err := getPemCert(token)
		if err != nil {
			return nil, err
		}

		return jwt.ParseRSAPublicKeyFromPEM([]byte(cert))
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, errors.New("invalid token")
}

func getPemCert(token *jwt.Token) (string, error) {
	cert := ""
	resp, err := http.Get(fmt.Sprintf("https://%s/.well-known/jwks.json", os.Getenv("AUTH0_DOMAIN")))
	if err != nil {
		return cert, err
	}
	defer resp.Body.Close()

	var jwks = struct {
		Keys []struct {
			Kty string   `json:"kty"`
			Kid string   `json:"kid"`
			Use string   `json:"use"`
			N   string   `json:"n"`
			E   string   `json:"e"`
			X5c []string `json:"x5c"`
		} `json:"keys"`
	}{}

	err = json.NewDecoder(resp.Body).Decode(&jwks)
	if err != nil {
		return cert, err
	}

	for k := range jwks.Keys {
		if token.Header["kid"] == jwks.Keys[k].Kid {
			cert = "-----BEGIN CERTIFICATE-----\n" + jwks.Keys[k].X5c[0] + "\n-----END CERTIFICATE-----"
		}
	}

	if cert == "" {
		return cert, errors.New("unable to find appropriate key")
	}

	return cert, nil
}
