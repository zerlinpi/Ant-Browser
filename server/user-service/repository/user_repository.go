package repository

import "github.com/zerlinpi/Ant-Browser/server/user-service/model"

// UserRepository abstracts user persistence.
// PostgreSQL implementation will be connected here.
type UserRepository struct {
}

func NewUserRepository() *UserRepository {
	return &UserRepository{}
}

func (r *UserRepository) Create(user *model.User) error {
	// TODO: save user to PostgreSQL
	return nil
}

func (r *UserRepository) FindByEmail(email string) (*model.User, error) {
	// TODO: query PostgreSQL
	return nil, nil
}
