package user

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/httpx"
)

type UserService interface {
	Register(ctx context.Context, input RegisterInput) (*User, error)
	Login(ctx context.Context, input LoginInput) (Session, error)
	Refresh(ctx context.Context, raw string) (Session, error)
	Logout(ctx context.Context, raw string) error
	ChangePassword(ctx context.Context, userID uint64, oldPassword, newPassword string) error
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

	session, err := h.service.Login(c.Request.Context(), request.ToInput())
	if err != nil {
		h.handleLoginError(c, err)
		return
	}

	c.JSON(http.StatusOK, LoginResponse{
		AccessToken:      session.AccessToken.Value,
		RefreshToken:     session.RefreshToken,
		TokenType:        "Bearer",
		ExpiresAt:        session.AccessToken.ExpiresAt,
		RefreshExpiresAt: session.RefreshExpiresAt,
	})
}

// Refresh 用刷新令牌换一对新令牌（旧刷新令牌立即失效）。
func (h *Handler) Refresh(c *gin.Context) {
	var request RefreshRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid request body")
		return
	}

	session, err := h.service.Refresh(c.Request.Context(), request.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrInvalidRefreshToken) {
			httpx.Abort(c, http.StatusUnauthorized, httpx.ErrorCodeUnauthorized, "invalid refresh token")
			return
		}
		httpx.Abort(c, http.StatusInternalServerError, httpx.ErrorCodeInternal, "internal server error")
		return
	}

	c.JSON(http.StatusOK, LoginResponse{
		AccessToken:      session.AccessToken.Value,
		RefreshToken:     session.RefreshToken,
		TokenType:        "Bearer",
		ExpiresAt:        session.AccessToken.ExpiresAt,
		RefreshExpiresAt: session.RefreshExpiresAt,
	})
}

// Logout 吊销刷新令牌（幂等）。
func (h *Handler) Logout(c *gin.Context) {
	var request LogoutRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid request body")
		return
	}
	if err := h.service.Logout(c.Request.Context(), request.RefreshToken); err != nil {
		httpx.Abort(c, http.StatusInternalServerError, httpx.ErrorCodeInternal, "internal server error")
		return
	}
	c.Status(http.StatusNoContent)
}

// ChangePassword 校验旧密码后更新，并吊销所有刷新令牌。
func (h *Handler) ChangePassword(c *gin.Context) {
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok {
		httpx.Abort(c, http.StatusUnauthorized, httpx.ErrorCodeUnauthorized, "unauthorized")
		return
	}

	var request ChangePasswordRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid request body")
		return
	}

	err := h.service.ChangePassword(c.Request.Context(), userID, request.OldPassword, request.NewPassword)
	if err != nil {
		if errors.Is(err, ErrInvalidOldPassword) {
			httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidCredentials, "old password is incorrect")
			return
		}
		httpx.Abort(c, http.StatusInternalServerError, httpx.ErrorCodeInternal, "internal server error")
		return
	}
	c.Status(http.StatusNoContent)
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
