package service

import "github.com/zerlinpi/Ant-Browser/server/user-service/model"

// UserService contains user related business logic.
type UserService struct{}

func NewUserService() *UserService {
	return &UserService{}
}

func (s *UserService) CreateUser(email string, passwordHash string) *model.User {
	return &model.User{
		Email: email,
		PasswordHash: passwordHash,
		Status: "active",
	}
}
