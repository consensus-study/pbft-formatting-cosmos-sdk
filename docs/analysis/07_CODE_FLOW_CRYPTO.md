# 암호화 및 서명 코드 플로우 분석

## 1. 암호화 구성요소

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      CRYPTO COMPONENTS                                       │
│                                                                              │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │                         KeyPair                                      │   │
│   │                                                                      │   │
│   │   ┌─────────────────┐          ┌─────────────────┐                 │   │
│   │   │  PrivateKey     │          │   PublicKey     │                 │   │
│   │   │  (ECDSA P-256)  │ ────────►│  (ECDSA P-256)  │                 │   │
│   │   └─────────────────┘          └─────────────────┘                 │   │
│   │                                                                      │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │                         Signer                                       │   │
│   │                                                                      │   │
│   │   Sign(data) ─────► Signature { R, S }                              │   │
│   │   Verify(data, sig) ─────► bool                                     │   │
│   │                                                                      │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │                      Hash Functions                                  │   │
│   │                                                                      │   │
│   │   Hash(data) ─────► SHA256                                          │   │
│   │   MerkleRoot(txs) ─────► Merkle Tree Root                           │   │
│   │                                                                      │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 2. 키 쌍 생성 플로우

```
┌─────────────────────────────────────────────────────────────────┐
│                   KEY GENERATION                                 │
│                                                                  │
│   ┌───────────────────────────────────────────────────────────┐ │
│   │                                                           │ │
│   │   crypto/rand ────► ecdsa.GenerateKey(P-256)              │ │
│   │                              │                            │ │
│   │                              ▼                            │ │
│   │                    ┌─────────────────┐                    │ │
│   │                    │    KeyPair      │                    │ │
│   │                    │                 │                    │ │
│   │                    │  PrivateKey ────┼───► 32 bytes       │ │
│   │                    │  PublicKey  ────┼───► 65 bytes       │ │
│   │                    │                 │    (uncompressed)  │ │
│   │                    └─────────────────┘                    │ │
│   │                                                           │ │
│   └───────────────────────────────────────────────────────────┘ │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// crypto/crypto.go - GenerateKeyPair()
func GenerateKeyPair() (*KeyPair, error) {
    // P-256 곡선으로 ECDSA 키 생성
    privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
    if err != nil {
        return nil, fmt.Errorf("failed to generate key: %w", err)
    }

    return &KeyPair{
        PrivateKey: privateKey,
        PublicKey:  &privateKey.PublicKey,
    }, nil
}

// 공개키를 바이트로 변환
func (kp *KeyPair) PublicKeyBytes() []byte {
    return elliptic.Marshal(kp.PublicKey.Curve,
        kp.PublicKey.X, kp.PublicKey.Y)
}

// 바이트에서 공개키 복원
func PublicKeyFromBytes(data []byte) (*ecdsa.PublicKey, error) {
    x, y := elliptic.Unmarshal(elliptic.P256(), data)
    if x == nil {
        return nil, errors.New("invalid public key")
    }
    return &ecdsa.PublicKey{
        Curve: elliptic.P256(),
        X:     x,
        Y:     y,
    }, nil
}
```

## 3. 서명 생성 플로우

```
┌─────────────────────────────────────────────────────────────────┐
│                      SIGN DATA                                   │
│                                                                  │
│   Data                                                           │
│     │                                                            │
│     ▼                                                            │
│   ┌─────────────────┐                                           │
│   │  SHA256(data)   │                                           │
│   └────────┬────────┘                                           │
│            │                                                     │
│            ▼                                                     │
│   ┌─────────────────┐     ┌─────────────────┐                   │
│   │     Hash        │     │   PrivateKey    │                   │
│   │   (32 bytes)    │────►│                 │                   │
│   └─────────────────┘     └────────┬────────┘                   │
│                                    │                             │
│                                    ▼                             │
│                          ┌─────────────────┐                    │
│                          │ ecdsa.Sign()    │                    │
│                          └────────┬────────┘                    │
│                                   │                              │
│                                   ▼                              │
│                          ┌─────────────────┐                    │
│                          │   Signature     │                    │
│                          │   { R, S }      │                    │
│                          └─────────────────┘                    │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// crypto/crypto.go - Sign()
func (s *DefaultSigner) Sign(data []byte) (*Signature, error) {
    // 데이터 해시
    hash := sha256.Sum256(data)

    // ECDSA 서명
    r, sg, err := ecdsa.Sign(rand.Reader, s.keyPair.PrivateKey, hash[:])
    if err != nil {
        return nil, fmt.Errorf("failed to sign: %w", err)
    }

    return &Signature{
        R: r.Bytes(),
        S: sg.Bytes(),
    }, nil
}

// Signature 타입
type Signature struct {
    R []byte  // ECDSA R 값
    S []byte  // ECDSA S 값
}

// 서명을 바이트로 직렬화
func (sig *Signature) Bytes() []byte {
    // R과 S를 연결 (각각 32바이트로 패딩)
    result := make([]byte, 64)
    copy(result[32-len(sig.R):32], sig.R)
    copy(result[64-len(sig.S):64], sig.S)
    return result
}

// 바이트에서 서명 복원
func SignatureFromBytes(data []byte) (*Signature, error) {
    if len(data) != 64 {
        return nil, errors.New("invalid signature length")
    }
    return &Signature{
        R: data[:32],
        S: data[32:],
    }, nil
}
```

## 4. 서명 검증 플로우

```
┌─────────────────────────────────────────────────────────────────┐
│                    VERIFY SIGNATURE                              │
│                                                                  │
│   Data              Signature            PublicKey              │
│     │                  │                    │                    │
│     ▼                  │                    │                    │
│   ┌─────────────────┐  │                    │                    │
│   │  SHA256(data)   │  │                    │                    │
│   └────────┬────────┘  │                    │                    │
│            │           │                    │                    │
│            ▼           ▼                    ▼                    │
│   ┌───────────────────────────────────────────────────────────┐ │
│   │                  ecdsa.Verify()                           │ │
│   │                                                           │ │
│   │   hash, (R, S), publicKey ────► true/false                │ │
│   │                                                           │ │
│   └───────────────────────────────────────────────────────────┘ │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// crypto/crypto.go - Verify()
func (s *DefaultSigner) Verify(data []byte, sig *Signature) bool {
    // 데이터 해시
    hash := sha256.Sum256(data)

    // big.Int로 변환
    r := new(big.Int).SetBytes(sig.R)
    sg := new(big.Int).SetBytes(sig.S)

    // ECDSA 검증
    return ecdsa.Verify(s.keyPair.PublicKey, hash[:], r, sg)
}

// 공개키로 서명 검증 (정적 함수)
func VerifyWithPublicKey(publicKey *ecdsa.PublicKey,
    data []byte, sig *Signature) bool {

    hash := sha256.Sum256(data)
    r := new(big.Int).SetBytes(sig.R)
    s := new(big.Int).SetBytes(sig.S)

    return ecdsa.Verify(publicKey, hash[:], r, s)
}
```

## 5. 해시 함수

### 5.1 단순 해시

```go
// crypto/crypto.go - Hash()
func Hash(data []byte) []byte {
    hash := sha256.Sum256(data)
    return hash[:]
}

// 여러 데이터 연결 후 해시
func HashMultiple(data ...[]byte) []byte {
    h := sha256.New()
    for _, d := range data {
        h.Write(d)
    }
    return h.Sum(nil)
}
```

### 5.2 머클 트리

```
┌─────────────────────────────────────────────────────────────────┐
│                      MERKLE TREE                                 │
│                                                                  │
│                    ┌─────────────────┐                          │
│                    │   Merkle Root   │                          │
│                    └────────┬────────┘                          │
│                             │                                    │
│              ┌──────────────┴──────────────┐                    │
│              │                             │                    │
│       ┌──────▼──────┐               ┌──────▼──────┐            │
│       │  Hash(0,1)  │               │  Hash(2,3)  │            │
│       └──────┬──────┘               └──────┬──────┘            │
│              │                             │                    │
│       ┌──────┴──────┐               ┌──────┴──────┐            │
│       │             │               │             │            │
│   ┌───▼───┐     ┌───▼───┐       ┌───▼───┐     ┌───▼───┐       │
│   │ Tx 0  │     │ Tx 1  │       │ Tx 2  │     │ Tx 3  │       │
│   └───────┘     └───────┘       └───────┘     └───────┘       │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// crypto/crypto.go - MerkleRoot()
func MerkleRoot(items [][]byte) []byte {
    if len(items) == 0 {
        return Hash(nil)
    }

    // 리프 노드 해시
    hashes := make([][]byte, len(items))
    for i, item := range items {
        hashes[i] = Hash(item)
    }

    // 트리 구성
    for len(hashes) > 1 {
        // 홀수 개면 마지막 복제
        if len(hashes)%2 != 0 {
            hashes = append(hashes, hashes[len(hashes)-1])
        }

        // 쌍으로 해시
        newHashes := make([][]byte, len(hashes)/2)
        for i := 0; i < len(hashes); i += 2 {
            combined := append(hashes[i], hashes[i+1]...)
            newHashes[i/2] = Hash(combined)
        }
        hashes = newHashes
    }

    return hashes[0]
}
```

## 6. PBFT 메시지 서명 플로우

### 6.1 메시지 서명

```
┌─────────────────────────────────────────────────────────────────┐
│                   SIGN PBFT MESSAGE                              │
│                                                                  │
│   PBFTMessage                                                    │
│       │                                                          │
│       ▼                                                          │
│   ┌─────────────────┐                                           │
│   │ Serialize       │                                           │
│   │ (View, Seq,     │                                           │
│   │  Digest, etc.)  │                                           │
│   └────────┬────────┘                                           │
│            │                                                     │
│            ▼                                                     │
│   ┌─────────────────┐                                           │
│   │   Sign(data)    │                                           │
│   └────────┬────────┘                                           │
│            │                                                     │
│            ▼                                                     │
│   ┌─────────────────┐                                           │
│   │ msg.Signature   │                                           │
│   │   = sig.Bytes() │                                           │
│   └─────────────────┘                                           │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// 메시지 서명 예시
func (e *Engine) signMessage(msg *pbftv1.PBFTMessage) error {
    // 서명할 데이터 생성
    data := e.messageDigest(msg)

    // 서명
    sig, err := e.signer.Sign(data)
    if err != nil {
        return err
    }

    // 메시지에 서명 설정
    msg.Signature = sig.Bytes()

    return nil
}

// 메시지 다이제스트 생성
func (e *Engine) messageDigest(msg *pbftv1.PBFTMessage) []byte {
    // 서명에 포함될 필드만 직렬화
    h := sha256.New()

    // Type
    binary.Write(h, binary.BigEndian, msg.Type)

    // View
    binary.Write(h, binary.BigEndian, msg.View)

    // Sequence
    binary.Write(h, binary.BigEndian, msg.Sequence)

    // Digest
    h.Write(msg.Digest)

    // NodeID
    h.Write([]byte(msg.NodeId))

    return h.Sum(nil)
}
```

### 6.2 메시지 서명 검증

```
┌─────────────────────────────────────────────────────────────────┐
│                  VERIFY PBFT MESSAGE                             │
│                                                                  │
│   PBFTMessage                                                    │
│       │                                                          │
│       ├──── msg.NodeId ────► lookup PublicKey                   │
│       │                                                          │
│       ├──── msg.Signature ──► SignatureFromBytes()              │
│       │                                                          │
│       └──── messageDigest(msg) ──► data                         │
│                                        │                         │
│                                        ▼                         │
│                              ┌─────────────────┐                │
│                              │ VerifyWithPubKey│                │
│                              │ (pubKey, data,  │                │
│                              │  signature)     │                │
│                              └────────┬────────┘                │
│                                       │                          │
│                                       ▼                          │
│                                  true / false                    │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// 메시지 서명 검증
func (e *Engine) verifyMessage(msg *pbftv1.PBFTMessage) bool {
    // 발신자 공개키 조회
    pubKey := e.getValidatorPublicKey(msg.NodeId)
    if pubKey == nil {
        return false
    }

    // 서명 복원
    sig, err := SignatureFromBytes(msg.Signature)
    if err != nil {
        return false
    }

    // 메시지 다이제스트 생성
    data := e.messageDigest(msg)

    // 검증
    return VerifyWithPublicKey(pubKey, data, sig)
}
```

## 7. 블록 해시 생성

```
┌─────────────────────────────────────────────────────────────────┐
│                   BLOCK HASH                                     │
│                                                                  │
│   Block                                                          │
│       │                                                          │
│       ├──── Header                                               │
│       │       ├── Height                                         │
│       │       ├── Timestamp                                      │
│       │       ├── ProposerID                                     │
│       │       ├── PrevHash                                       │
│       │       └── TxHash (MerkleRoot)                           │
│       │                                                          │
│       └──── Transactions[] ──► MerkleRoot() ──► TxHash          │
│                                                                  │
│   HeaderBytes = Serialize(Header)                               │
│       │                                                          │
│       ▼                                                          │
│   BlockHash = SHA256(HeaderBytes)                               │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// 블록 해시 계산
func (b *Block) ComputeHash() []byte {
    // 트랜잭션 머클 루트 계산
    txs := make([][]byte, len(b.Transactions))
    for i, tx := range b.Transactions {
        txs[i] = tx.Data
    }
    b.Header.TxHash = MerkleRoot(txs)

    // 헤더 직렬화
    h := sha256.New()

    // Height
    binary.Write(h, binary.BigEndian, b.Header.Height)

    // Timestamp
    binary.Write(h, binary.BigEndian, b.Header.Timestamp.UnixNano())

    // ProposerID
    h.Write([]byte(b.Header.ProposerID))

    // PrevHash
    h.Write(b.Header.PrevHash)

    // TxHash
    h.Write(b.Header.TxHash)

    // AppHash
    h.Write(b.Header.AppHash)

    return h.Sum(nil)
}
```

## 8. 암호화 관련 상수

```go
const (
    // 해시 크기
    HashSize = 32  // SHA256

    // 서명 크기
    SignatureSize = 64  // R(32) + S(32)

    // 공개키 크기 (비압축)
    PublicKeySize = 65  // 04 + X(32) + Y(32)

    // 공개키 크기 (압축)
    CompressedPublicKeySize = 33

    // 개인키 크기
    PrivateKeySize = 32
)
```

## 9. 보안 고려사항

| 항목 | 설명 |
|------|------|
| 난수 생성 | `crypto/rand` 사용 (암호학적 안전) |
| 해시 함수 | SHA-256 (충돌 저항성) |
| 서명 알고리즘 | ECDSA P-256 (NIST 표준) |
| 키 저장 | 메모리 내 보관 (프로덕션에서는 HSM 권장) |
| 타이밍 공격 | constant-time 비교 사용 권장 |
