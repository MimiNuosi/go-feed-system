package user

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/httpx"
	"go-feed-system/pkg/token"
)

type UserService interface {
	Register(ctx context.Context, input RegisterInput) (*User, error)
	Login(ctx context.Context, input LoginInput) (token.AccessToken, error)
	GetByID(ctx context.Context, id uint64) (*User, error)
}

type Handler struct {
	service UserService
}

func NewHandler(service UserService) *Handler {
	return &Handler{
		service: service,
	}
}

func (h *Handler) Register(c *gin.Context) {
	var request RegisterRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeInvalidArgument),
			httpx.ErrorCodeInvalidArgument,
			"invalid request body",
		)
		return
	}

	createdUser, err := h.service.Register(c.Request.Context(), request.ToInput())
	if err != nil {
		h.handleRegisterError(c, err)
		return
	}

	c.JSON(http.StatusCreated, RegisterResponse{
		User: NewUserResponse(createdUser),
	})
}

func (h *Handler) Login(c *gin.Context) {
	var request LoginRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeInvalidArgument),
			httpx.ErrorCodeInvalidArgument,
			"invalid request body",
		)
		return
	}

	accessToken, err := h.service.Login(c.Request.Context(), request.ToInput())
	if err != nil {
		h.handleLoginError(c, err)
		return
	}

	c.JSON(http.StatusOK, LoginResponse{
		AccessToken: accessToken.Value,
		TokenType:   "Bearer",
		ExpiresAt:   accessToken.ExpiresAt,
	})
}

func (h *Handler) Me(c *gin.Context) {
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok {
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeUnauthorized),
			httpx.ErrorCodeUnauthorized,
			"unauthorized",
		)
		return
	}

	currentUser, err := h.service.GetByID(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.Abort(
				c,
				httpx.StatusCode(httpx.ErrorCodeUnauthorized),
				httpx.ErrorCodeUnauthorized,
				"unauthorized",
			)
			return
		}

		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeInternal),
			httpx.ErrorCodeInternal,
			"internal server error",
		)
		return
	}

	c.JSON(http.StatusOK, NewUserResponse(currentUser))
}

func (h *Handler) handleRegisterError(c *gin.Context, err error) {
	if errors.Is(err, ErrConflict) {
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeConflict),
			httpx.ErrorCodeConflict,
			"user already exists",
		)
		return
	}

	httpx.Abort(
		c,
		httpx.StatusCode(httpx.ErrorCodeInternal),
		httpx.ErrorCodeInternal,
		"internal server error",
	)
}

func (h *Handler) handleLoginError(c *gin.Context, err error) {
	if errors.Is(err, ErrInvalidCredentials) {
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeInvalidCredentials),
			httpx.ErrorCodeInvalidCredentials,
			"invalid email or password",
		)
		return
	}

	httpx.Abort(
		c,
		httpx.StatusCode(httpx.ErrorCodeInternal),
		httpx.ErrorCodeInternal,
		"internal server error",
	)
}
