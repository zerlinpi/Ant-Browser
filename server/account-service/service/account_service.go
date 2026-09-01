package service

import "github.com/zerlinpi/Ant-Browser/server/account-service/model"

// AccountService manages commerce accounts.
type AccountService struct{}

func NewAccountService() *AccountService {
	return &AccountService{}
}

func (s *AccountService) Create(account model.Account) model.Account {
	return account
}

func (s *AccountService) List(workspaceID uint64) []model.Account {
	return []model.Account{}
}
