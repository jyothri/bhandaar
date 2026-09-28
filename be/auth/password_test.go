package auth

import "testing"

// agentserverHash was made by agentserver's auth.HashPassword (with its cost
// lowered to m=64,t=1,p=1) for "correct horse battery". be must accept the
// hashes agentserver's `user add` and `user passwd` write.
const agentserverHash = "$argon2id$v=19$m=64,t=1,p=1$Ofm8BW4SjXoahi/VXrxl1g$ZXBFmZAtVEy+IQDS+l2Ne4Btux29zQaeArj/TlLktT0"

func TestVerifyAgentserverHash(t *testing.T) {
	ok, err := VerifyPassword(agentserverHash, "correct horse battery")
	if err != nil || !ok {
		t.Fatalf("VerifyPassword(right password) = %v, %v; want true", ok, err)
	}
	ok, err = VerifyPassword(agentserverHash, "correct horse batterY")
	if err != nil || ok {
		t.Fatalf("VerifyPassword(wrong password) = %v, %v; want false", ok, err)
	}
}

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("a long enough password")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifyPassword(hash, "a long enough password"); err != nil || !ok {
		t.Fatalf("VerifyPassword = %v, %v; want true", ok, err)
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	for _, hash := range []string{
		"",
		"plain",
		"$argon2i$v=19$m=64,t=1,p=1$Ofm8BW4SjXoahi/VXrxl1g$ZXBFmZAtVEy+IQDS+l2Ne4Btux29zQaeArj/TlLktT0",
		"$argon2id$v=18$m=64,t=1,p=1$Ofm8BW4SjXoahi/VXrxl1g$ZXBFmZAtVEy+IQDS+l2Ne4Btux29zQaeArj/TlLktT0",
		"$argon2id$v=19$m=0,t=1,p=1$Ofm8BW4SjXoahi/VXrxl1g$ZXBFmZAtVEy+IQDS+l2Ne4Btux29zQaeArj/TlLktT0",
		"$argon2id$v=19$m=64,t=1,p=1$!!$ZXBFmZAtVEy+IQDS+l2Ne4Btux29zQaeArj/TlLktT0",
		"$argon2id$v=19$m=64,t=1,p=1$Ofm8BW4SjXoahi/VXrxl1g$",
	} {
		if ok, err := VerifyPassword(hash, "x"); err == nil || ok {
			t.Errorf("VerifyPassword(%q) = %v, %v; want an error", hash, ok, err)
		}
	}
}
