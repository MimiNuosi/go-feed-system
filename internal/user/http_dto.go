package user

import "time"

type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=32"`
	Email    string `json:"email" binding:"required,email,max=254"`
	Password string `json:"password" binding:"required,min=8,max=72"`
}

func (r RegisterRequest) ToInput() RegisterInput {
	return RegisterInput{
		Username: r.Username,
		Email:    r.Email,
		Password: r.Password,
	}
}

type RegisterResponse struct {
	User UserResponse `json:"user"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (r LoginRequest) ToInput() LoginInput {
	return LoginInput{
		Email:    r.Email,
		Password: r.Password,
	}
}

type LoginResponse struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"expires_at"`
}
