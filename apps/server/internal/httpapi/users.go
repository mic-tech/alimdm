package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"ali-mdm/server/internal/auth"
	"ali-mdm/server/internal/store"
)

// minPasswordLen is the shortest password the console will accept. Deliberately
// modest: these are small-fleet operator accounts, not internet-facing signups.
const minPasswordLen = 8

// publicUser is the account shape the console sees — never the password hash.
type publicUserDTO struct {
	Email     string `json:"email"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

func publicUser(o *store.Operator) publicUserDTO {
	return publicUserDTO{Email: o.Email, Name: o.Name, Role: o.Role, CreatedAt: o.CreatedAt}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// normaliseEmail lower-cases and trims so that lookups and uniqueness checks
// agree regardless of how the address was typed.
func normaliseEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func validEmail(e string) bool {
	at := strings.Index(e, "@")
	return at > 0 && at < len(e)-1 && !strings.ContainsAny(e, " /\\")
}

// pathEmail reads the {email} path segment, which the console percent-encodes.
func pathEmail(r *http.Request) string { return normaliseEmail(r.PathValue("email")) }

// ── Own profile ──────────────────────────────────────────────────────────────

func (s *Server) getMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, publicUser(operatorFrom(r.Context())))
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	me := operatorFrom(r.Context())
	var req struct {
		Email *string `json:"email"`
		Name  *string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}

	next := *me
	if req.Name != nil {
		next.Name = strings.TrimSpace(*req.Name)
	}
	if req.Email != nil {
		email := normaliseEmail(*req.Email)
		if !validEmail(email) {
			writeErr(w, http.StatusBadRequest, "enter a valid email address")
			return
		}
		if email != normaliseEmail(me.Email) {
			if _, err := s.st.GetOperator(email); err == nil {
				writeErr(w, http.StatusConflict, "that email is already in use")
				return
			}
		}
		next.Email = email
	}

	if err := s.st.UpdateOperator(me.Email, &next); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save profile")
		return
	}

	// Changing your own email moves the subject the session token points at, so
	// hand back a token minted for the new address.
	resp := map[string]any{"user": publicUser(&next)}
	if !strings.EqualFold(next.Email, me.Email) {
		tok, _ := s.signer.Sign(auth.Claims{
			Sub: next.Email, Kind: "operator", Email: next.Email, Role: next.Role,
			Exp: time.Now().Add(12 * time.Hour).Unix(),
		})
		resp["token"] = tok
	}
	writeJSON(w, resp)
}

func (s *Server) changeMyPassword(w http.ResponseWriter, r *http.Request) {
	me := operatorFrom(r.Context())
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	if !auth.VerifyPassword(req.CurrentPassword, me.PasswordHash) {
		writeErr(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		writeErr(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	if err := s.st.UpdateOperatorPassword(me.Email, hash); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save password")
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// ── Account administration ───────────────────────────────────────────────────

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	ops, err := s.st.ListOperators()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list users")
		return
	}
	out := make([]publicUserDTO, 0, len(ops))
	for i := range ops {
		out = append(out, publicUser(&ops[i]))
	}
	writeJSON(w, out)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	email := normaliseEmail(req.Email)
	if !validEmail(email) {
		writeErr(w, http.StatusBadRequest, "enter a valid email address")
		return
	}
	if req.Role == "" {
		req.Role = store.RoleOperator
	}
	if !store.ValidRole(req.Role) {
		writeErr(w, http.StatusBadRequest, "role must be admin or operator")
		return
	}
	if len(req.Password) < minPasswordLen {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if _, err := s.st.GetOperator(email); err == nil {
		writeErr(w, http.StatusConflict, "that email is already in use")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	op := &store.Operator{
		Email: email, Name: strings.TrimSpace(req.Name), Role: req.Role,
		PasswordHash: hash, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.st.CreateOperator(op); err != nil {
		writeErr(w, http.StatusConflict, "that email is already in use")
		return
	}
	s.record(r, "user_created", store.EventWarn, "",
		"Added "+op.Email+" as "+string(op.Role))
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, publicUser(op))
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	me := operatorFrom(r.Context())
	target, err := s.st.GetOperator(pathEmail(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such user")
		return
	}
	var req struct {
		Email *string `json:"email"`
		Name  *string `json:"name"`
		Role  *string `json:"role"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}

	next := *target
	if req.Name != nil {
		next.Name = strings.TrimSpace(*req.Name)
	}
	if req.Email != nil {
		email := normaliseEmail(*req.Email)
		if !validEmail(email) {
			writeErr(w, http.StatusBadRequest, "enter a valid email address")
			return
		}
		if email != normaliseEmail(target.Email) {
			if _, err := s.st.GetOperator(email); err == nil {
				writeErr(w, http.StatusConflict, "that email is already in use")
				return
			}
		}
		next.Email = email
	}
	if req.Role != nil {
		if !store.ValidRole(*req.Role) {
			writeErr(w, http.StatusBadRequest, "role must be admin or operator")
			return
		}
		// Demoting the only remaining admin would lock account management for
		// everyone, so refuse it rather than leave the console unadministrable.
		if target.Role == store.RoleAdmin && *req.Role != store.RoleAdmin {
			others, err := s.st.CountAdminsExcept(target.Email)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "could not check administrators")
				return
			}
			if others == 0 {
				writeErr(w, http.StatusConflict, "this is the last administrator — promote another user first")
				return
			}
		}
		next.Role = *req.Role
	}

	if err := s.st.UpdateOperator(target.Email, &next); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save user")
		return
	}

	resp := map[string]any{"user": publicUser(&next)}
	// An admin renaming their own account needs a token for the new subject.
	if strings.EqualFold(target.Email, me.Email) && !strings.EqualFold(next.Email, me.Email) {
		tok, _ := s.signer.Sign(auth.Claims{
			Sub: next.Email, Kind: "operator", Email: next.Email, Role: next.Role,
			Exp: time.Now().Add(12 * time.Hour).Unix(),
		})
		resp["token"] = tok
	}
	writeJSON(w, resp)
}

func (s *Server) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	target, err := s.st.GetOperator(pathEmail(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such user")
		return
	}
	var req struct {
		NewPassword string `json:"new_password"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	if err := s.st.UpdateOperatorPassword(target.Email, hash); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save password")
		return
	}
	s.record(r, "user_password_reset", store.EventWarn, "", "Reset the password for "+target.Email)
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	me := operatorFrom(r.Context())
	email := pathEmail(r)
	target, err := s.st.GetOperator(email)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such user")
		return
	}
	if strings.EqualFold(target.Email, me.Email) {
		writeErr(w, http.StatusConflict, "you cannot delete your own account")
		return
	}
	if target.Role == store.RoleAdmin {
		others, err := s.st.CountAdminsExcept(target.Email)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not check administrators")
			return
		}
		if others == 0 {
			writeErr(w, http.StatusConflict, "this is the last administrator — promote another user first")
			return
		}
	}
	if err := s.st.DeleteOperator(target.Email); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not delete user")
		return
	}
	s.record(r, "user_deleted", store.EventWarn, "", "Deleted the account "+target.Email)
	writeJSON(w, map[string]string{"status": "deleted"})
}
