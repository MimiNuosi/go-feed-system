package user

import (
	"fmt"
	"time"
)

type RegisterInput struct {
	Username string
	Email    string
	Password string
}

type LoginInput struct {
	Email    string
	Password string
}

type UserResponse struct {
	ID        uint64    `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func NewUserResponse(user *User) UserResponse {
	response := UserResponse{
		ID:        user.ID,
		Username:  user.Username,
		Email:     user.Email,
		CreatedAt: user.CreatedAt,
	}
	if user.AvatarKey != "" {
		response.AvatarURL = fmt.Sprintf("/api/v1/users/%d/avatar", user.ID)
	}
	return response
}
