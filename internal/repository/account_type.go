package repository

import (
	"encoding/json"
	"fmt"
	"strings"
)

type AccountType uint8

const (
	AccountTypeLocal   AccountType = 0x00
	AccountTypeService AccountType = 0x01
	AccountTypeOIDC    AccountType = 0x02
)

func (a AccountType) String() string {
	switch a {
	case AccountTypeLocal:
		return "local"
	case AccountTypeService:
		return "service_account"
	case AccountTypeOIDC:
		return "oidc"
	default:
		return "local"
	}
}

func ParseAccountType(s string) (AccountType, error) {
	switch strings.ToLower(s) {
	case "local", "0":
		return AccountTypeLocal, nil
	case "service", "service_account", "1":
		return AccountTypeService, nil
	case "oidc", "2":
		return AccountTypeOIDC, nil
	default:
		return AccountTypeLocal, fmt.Errorf("invalid account type '%s', must be 'local', 'service_account', or 'oidc'", s)
	}
}

func (a AccountType) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.String())
}

func (a *AccountType) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*a = AccountTypeLocal
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if s == "" {
			*a = AccountTypeLocal
			return nil
		}
		parsed, err := ParseAccountType(s)
		if err != nil {
			return err
		}
		*a = parsed
		return nil
	}
	var i uint8
	if err := json.Unmarshal(data, &i); err == nil {
		*a = AccountType(i)
		return nil
	}
	return fmt.Errorf("invalid account type")
}

func (u User) IsServiceAccount() bool {
	return u.AccountType == AccountTypeService
}

func (u User) IsOIDC() bool {
	return u.AccountType == AccountTypeOIDC
}

func (u User) IsLocal() bool {
	return u.AccountType == AccountTypeLocal
}
