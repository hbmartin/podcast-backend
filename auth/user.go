package auth

// User is the authenticated identity stored in the request context by
// TokenAuthMiddleware. UUID is the public user identifier (users.uuid).
type User struct {
	UUID  string
	Email string
	Scope string
}
