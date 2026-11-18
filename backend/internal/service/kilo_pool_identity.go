package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Identity material belongs to the Kilo identity, not to its replaceable GPT
// slot. It is encrypted at rest and used only by the authenticated executor.
func (s *KiloService) kiloIdentityMaterial(ctx context.Context, id int64, key, userID, email string) (*KiloExecutorIdentityCredential, error) {
	var sessionCipher, credentialCipher string
	if err := s.db.QueryRowContext(ctx, `SELECT identity_session_encrypted,identity_credential_encrypted FROM pk_kilo_prepared_accounts WHERE id=$1 AND identity_status='active'`, id).Scan(&sessionCipher, &credentialCipher); err != nil {
		return nil, err
	}
	out := &KiloExecutorIdentityCredential{APIKey: key, UserID: userID, Email: email}
	if sessionCipher != "" {
		plain, err := s.encryptor.Decrypt(sessionCipher)
		if err != nil {
			return nil, errors.New("Kilo身份会话无法解密")
		}
		out.SessionMaterial = plain
	}
	if credentialCipher != "" {
		plain, err := s.encryptor.Decrypt(credentialCipher)
		if err != nil {
			return nil, errors.New("Kilo身份登录材料无法解密")
		}
		var material KiloExecutorCredential
		if json.Unmarshal([]byte(plain), &material) != nil || material.Email == "" || material.Password == "" {
			return nil, errors.New("Kilo身份登录材料格式无效")
		}
		out.Credential = &material
	}
	if out.SessionMaterial == "" && out.Credential == nil {
		return nil, kiloOnboardingFail("AUTH_SESSION_EXPIRED", "该历史Kilo身份缺少可恢复的独立登录会话", "恢复原Kilo身份会话后重试，不能使用新GPT账号创建替代身份", false)
	}
	return out, nil
}

func (s *KiloService) saveKiloIdentityMaterial(ctx context.Context, job *kiloClaim, state *kiloOnboardingRuntime) error {
	if job.PreparedID <= 0 {
		return nil
	}
	var sessionCipher, credentialCipher string
	var err error
	if state.IdentitySessionMaterial != "" {
		sessionCipher, err = s.encryptor.Encrypt(state.IdentitySessionMaterial)
		if err != nil {
			return errors.New("Kilo身份会话加密失败")
		}
	}
	if state.IdentityCredential != nil && !job.ReusedIdentity {
		raw, e := json.Marshal(state.IdentityCredential)
		if e != nil {
			return e
		}
		credentialCipher, err = s.encryptor.Encrypt(string(raw))
		if err != nil {
			return errors.New("Kilo身份登录材料加密失败")
		}
	}
	if sessionCipher == "" && credentialCipher == "" {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = LockKiloOnboardingLease(withKiloClaimLease(ctx, job), tx); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET identity_session_encrypted=CASE WHEN $3='' THEN identity_session_encrypted ELSE $3 END,identity_credential_encrypted=CASE WHEN identity_credential_encrypted='' AND $4<>'' THEN $4 ELSE identity_credential_encrypted END,updated_at=NOW() WHERE id=$1 AND binding_job_id=$2 AND identity_status='active'`, job.PreparedID, job.ID, sessionCipher, credentialCipher)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return kiloConflict("Kilo身份已由其他任务接管")
	}
	return tx.Commit()
}

// Legacy identities can retain their original login material before the first
// automated detach. Never copy a replacement GPT's credentials into the Kilo
// identity: only the original Kilo email is eligible for this one-time capture.
func (s *KiloService) retainLegacyKiloLogin(ctx context.Context, p kiloPoolBinding, source *Account) error {
	var email, cipher string
	if err := s.db.QueryRowContext(ctx, `SELECT email,identity_credential_encrypted FROM pk_kilo_prepared_accounts WHERE id=$1 AND source_account_id=$2 AND binding_revision=$3`, p.ID, p.SourceID, p.Revision).Scan(&email, &cipher); err != nil {
		return err
	}
	if cipher != "" || !strings.EqualFold(strings.TrimSpace(email), kiloSourceEmail(source)) {
		return nil
	}
	material, err := s.reauth.ReadKiloLoginMaterial(ctx, source.ID)
	if err != nil {
		return nil
	}
	if !strings.EqualFold(material.Email, email) {
		return nil
	}
	raw, err := json.Marshal(KiloExecutorCredential{Email: material.Email, Password: material.Password, TOTPSecret: material.TOTPSecret})
	if err != nil {
		return err
	}
	encrypted, err := s.encryptor.Encrypt(string(raw))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET identity_credential_encrypted=$4,updated_at=NOW() WHERE id=$1 AND source_account_id=$2 AND binding_revision=$3 AND identity_credential_encrypted=''`, p.ID, p.SourceID, p.Revision, encrypted)
	return err
}
