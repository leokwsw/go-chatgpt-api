package ninja

type Error struct {
	StatusCode int
	Details    string
	Remark     string
}

func NewError(statusCode int, details string, remark string) *Error {
	return &Error{
		StatusCode: statusCode,
		Details:    details,
		Remark:     remark,
	}
}

type ErrorResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type AuthTokenResponse struct {
	User         AuthTokenUser `json:"user"`
	Expires      string        `json:"expires"`
	AccessToken  string        `json:"accessToken"`
	AuthProvider string        `json:"authProvider"`
	SessionToken string        `json:"session_token"`
}

type AuthTokenUser struct {
	Id           string   `json:"id"`
	Name         string   `json:"name"`
	Email        string   `json:"email"`
	Image        string   `json:"image"`
	Picture      string   `json:"picture"`
	Idp          string   `json:"idp"`
	Iat          int      `json:"iat"`
	Mfa          bool     `json:"mfa"`
	Groups       []string `json:"groups"`
	IntercomHash string   `json:"intercom_hash"`
}
