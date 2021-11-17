/*
 * Copyright (c) 2021 XLAB d.o.o
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package abe

import (
    "crypto/aes"
    cbc "crypto/cipher"
    "crypto/rand"
    "crypto/sha256"
    "fmt"
    "math/big"
    "io"
    "github.com/fentec-project/bn256"
    "github.com/fentec-project/gofe/data"
    "github.com/fentec-project/gofe/sample"
)

// This is a ciphertext policy (CP) multi-authority (MA) attribute based
// encryption (ABE) scheme based on the paper "Decentralizing Attribute-Based
// Encryption" by Allison Lewko and Brent Waters, accessible at
// (https://eprint.iacr.org/2010/351.pdf).
//
// This scheme enables encryption based on a boolean expression determining
// which attributes are needed for an entity to be able to decrypt, where the
// attributes can be spread across many different authorities, eliminating the
// need for a central authority. Secret keys, each connected to a single
// attribute, are generated by the relevant authorities, such that only a set
// of keys whose attributes are sufficient according to the boolean formula can
// decrypt the message.

// MAABE represents a MAABE scheme.
type MAABE struct {
    P *big.Int
    g1 *bn256.G1
    g2 *bn256.G2
    gt *bn256.GT
}

// NewMAABE configures a new instance of the scheme.
func NewMAABE() *MAABE {
    gen1 := new(bn256.G1).ScalarBaseMult(big.NewInt(1))
    gen2 := new(bn256.G2).ScalarBaseMult(big.NewInt(1))
    return &MAABE{
            P: bn256.Order,
            g1: gen1,
            g2: gen2,
            gt: bn256.Pair(gen1, gen2),
    }
}

// MAABEPubKey represents a public key for an authority.
type MAABEPubKey struct {
    Attribs []string
    EggToAlpha map[string]*bn256.GT
    GToY map[string]*bn256.G2
}

// MAABESecKey represents a secret key for an authority.
type MAABESecKey struct {
    Attribs []string
    Alpha map[string]*big.Int
    Y map[string]*big.Int
}

// MAABEAuth represents an authority in the MAABE scheme.
type MAABEAuth struct {
    ID string
    Maabe *MAABE
    Pk *MAABEPubKey
    Sk *MAABESecKey
}

// NewMAABEAuth configures a new instance of an authority and generates its
// public and secret keys for the given set of attributes. In case of a failed
// procedure an error is returned.
func (a *MAABE) NewMAABEAuth(id string, attribs []string) (*MAABEAuth, error) {
    numattrib := len(attribs)
    // sanity checks
    if numattrib == 0 {
        return nil, fmt.Errorf("empty set of authority attributes")
    }
    if len(id) == 0 {
        return nil, fmt.Errorf("empty id string")
    }
    // rand generator
    sampler := sample.NewUniform(a.P)
    // generate seckey
    alphaI, err := data.NewRandomVector(numattrib, sampler)
    if err != nil {
        return nil, err
    }
    yI, err := data.NewRandomVector(numattrib, sampler)
    if err != nil {
        return nil, err
    }
    alpha := make(map[string]*big.Int)
    y := make(map[string]*big.Int)
    for i, at := range attribs {
        alpha[at] = alphaI[i]
        y[at] = yI[i]
    }
    // generate pubkey
    eggToAlpha := make(map[string]*bn256.GT)
    gToY := make(map[string]*bn256.G2)
    for _, at := range attribs {
        eggToAlpha[at] = new(bn256.GT).ScalarMult(a.gt, alpha[at])
        gToY[at] = new(bn256.G2).ScalarMult(a.g2, y[at])
    }
    sk := &MAABESecKey{Attribs: attribs, Alpha: alpha, Y: y}
    pk := &MAABEPubKey{Attribs: attribs, EggToAlpha: eggToAlpha, GToY: gToY}
    return &MAABEAuth{
        ID: id,
        Maabe: a,
        Pk: pk,
        Sk: sk,
    }, nil
}

// PubKeys is a getter function that returns a copy of the authority's public
// keys.
func (auth *MAABEAuth) PubKeys() *MAABEPubKey {
    newEggToAlpha := make(map[string]*bn256.GT)
    newGToY := make(map[string]*bn256.G2)
    newAttribs := make([]string, len(auth.Pk.Attribs))
    copy(newAttribs, auth.Pk.Attribs)
    for at, gt := range auth.Pk.EggToAlpha {
        newEggToAlpha[at] = new(bn256.GT).Set(gt)
    }
    for at, g2 := range auth.Pk.GToY {
        newGToY[at] = new(bn256.G2).Set(g2)
    }
    return &MAABEPubKey{
        Attribs: newAttribs,
        EggToAlpha: newEggToAlpha,
        GToY: newGToY,
    }
}

// AddAttribute generates public and secret keys for a new attribute that is
// given as input. In case of a failed procedure an error is returned, and nil
// otherwise.
func (auth *MAABEAuth) AddAttribute(attrib string) error {
    // sanity checks
    if len(attrib) == 0 {
        return fmt.Errorf("attribute cannot be an empty string")
    }
    if auth.Maabe == nil {
        return fmt.Errorf("MAABE struct cannot be nil")
    }
    // attribute should not already exist, separate function
    if auth.Sk.Alpha[attrib] != nil || auth.Sk.Y[attrib] != nil {
        return fmt.Errorf("attribute already exists")
    }
    // generate secret key
    sampler := sample.NewUniform(auth.Maabe.P)
    skVals, err := data.NewRandomVector(2, sampler)
    if err != nil {
        return err
    }
    alpha := skVals[0]
    y := skVals[1]
    // generate public key
    eggToAlpha := new(bn256.GT).ScalarMult(auth.Maabe.gt, alpha)
    gToY := new(bn256.G2).ScalarMult(auth.Maabe.g2, y)
    // add keys to authority
    auth.Sk.Alpha[attrib] = alpha
    auth.Sk.Y[attrib] = y
    auth.Pk.EggToAlpha[attrib] = eggToAlpha
    auth.Pk.GToY[attrib] = gToY
    auth.Sk.Attribs = append(auth.Sk.Attribs, attrib)
    auth.Pk.Attribs = append(auth.Pk.Attribs, attrib)
    return nil
}

// RegenerateKey generates public and secret keys for an already existing
// attribute that is given as input. In case of a failed procedure an error is
// returned. It is meant to be used in case only a part of the authority's
// secret keys get compromised. Note that the new public keys have to be
// distributed and messages that were encrypted with a policy that contains
// this attribute have to also be reencrypted.
func (auth *MAABEAuth) RegenerateKey(attrib string) error {
    // sanity checks
    if len(attrib) == 0 {
        return fmt.Errorf("attribute cannot be an empty string")
    }
    if auth.Maabe == nil {
        return fmt.Errorf("MAABE struct cannot be nil")
    }
    // attribute must already exist
    if auth.Sk.Alpha[attrib] == nil || auth.Sk.Y[attrib] == nil {
        return fmt.Errorf("attribute does not exist yet")
    }
    // generate secret key
    sampler := sample.NewUniform(auth.Maabe.P)
    skVals, err := data.NewRandomVector(2, sampler)
    if err != nil {
        return err
    }
    alpha := skVals[0]
    y := skVals[1]
    // generate public key
    eggToAlpha := new(bn256.GT).ScalarMult(auth.Maabe.gt, alpha)
    gToY := new(bn256.G2).ScalarMult(auth.Maabe.g2, y)
    // add keys to authority
    auth.Sk.Alpha[attrib] = alpha
    auth.Sk.Y[attrib] = y
    auth.Pk.EggToAlpha[attrib] = eggToAlpha
    auth.Pk.GToY[attrib] = gToY
    return nil
}

// MAABECipher represents a ciphertext of a MAABE scheme.
type MAABECipher struct {
    C0 *bn256.GT
    C1x map[string]*bn256.GT
    C2x map[string]*bn256.G2
    C3x map[string]*bn256.G2
    Msp *MSP
    SymEnc []byte // symmetric encryption of the string message
    Iv []byte // initialization vector for symmetric encryption
}

// Encrypt takes an input message in string form, a MSP struct representing the
// decryption policy and a list of public keys of the relevant authorities. It
// returns a ciphertext consisting of an AES encrypted message with the secret
// key encrypted according to the MAABE scheme. In case of a failed procedure
// an error is returned.
func (a *MAABE) Encrypt(msg string, msp *MSP, pks []*MAABEPubKey) (*MAABECipher, error) {
    // sanity checks
    if len(msp.Mat) == 0 || len(msp.Mat[0]) == 0 {
        return nil, fmt.Errorf("empty msp matrix")
    }
    mspRows := msp.Mat.Rows()
    mspCols := msp.Mat.Cols()
    attribs := make(map[string]bool)
    for _, i := range msp.RowToAttrib {
        if attribs[i] {
            return nil, fmt.Errorf("some attributes correspond to" +
            "multiple rows of the MSP struct, the scheme is not secure")
        }
        attribs[i] = true
    }
    if len(msg) == 0 {
        return nil, fmt.Errorf("message cannot be empty")
    }
    // msg is encrypted with AES-CBC with a random key that is encrypted with
    // MA-ABE
    // generate secret key
    _, symKey, err := bn256.RandomGT(rand.Reader)
    if err != nil {
        return nil, err
    }
    // generate new AES-CBC params
    keyCBC := sha256.Sum256([]byte(symKey.String()))
    cipherAES, err := aes.NewCipher(keyCBC[:])
    if err != nil {
        return nil, err
    }
    iv := make([]byte, cipherAES.BlockSize())
    _, err = io.ReadFull(rand.Reader, iv)
    if err != nil {
        return nil, err
    }
    encrypterCBC := cbc.NewCBCEncrypter(cipherAES, iv)
    // interpret msg as a byte array and pad it according to PKCS7 standard
    msgByte := []byte(msg)
    padLen := cipherAES.BlockSize() - (len(msgByte) % cipherAES.BlockSize())
    msgPad := make([]byte, len(msgByte) + padLen)
    copy(msgPad, msgByte)
    for i := len(msgByte); i < len(msgPad); i++ {
        msgPad[i] = byte(padLen)
    }
    // encrypt data
    symEnc := make([]byte, len(msgPad))
    encrypterCBC.CryptBlocks(symEnc, msgPad)

    // now encrypt symKey with MA-ABE
    // rand generator
    sampler := sample.NewUniform(a.P)
    // pick random vector v with random s as first element
    v, err := data.NewRandomVector(mspCols, sampler)
    if err != nil {
        return nil, err
    }
    s := v[0]
    if err != nil {
        return nil, err
    }
    lambdaI, err := msp.Mat.MulVec(v)
    if err != nil {
        return nil, err
    }
    if len(lambdaI) != mspRows {
        return nil, fmt.Errorf("wrong lambda len")
    }
    lambda := make(map[string]*big.Int)
    for i, at := range msp.RowToAttrib {
        lambda[at] = lambdaI[i]
    }
    // pick random vector w with 0 as first element
    w, err := data.NewRandomVector(mspCols, sampler)
    if err != nil {
        return nil, err
    }
    w[0] = big.NewInt(0)
    omegaI, err := msp.Mat.MulVec(w)
    if err != nil {
        return nil, err
    }
    if len(omegaI) != mspRows {
        return nil, fmt.Errorf("wrong omega len")
    }
    omega := make(map[string]*big.Int)
    for i, at := range msp.RowToAttrib {
        omega[at] = omegaI[i]
    }
    // calculate ciphertext
    c0 := new(bn256.GT).Add(symKey, new(bn256.GT).ScalarMult(a.gt, s))
    c1 := make(map[string]*bn256.GT)
    c2 := make(map[string]*bn256.G2)
    c3 := make(map[string]*bn256.G2)
    // get randomness
    rI, err := data.NewRandomVector(mspRows, sampler)
    r := make(map[string]*big.Int)
    for i, at := range msp.RowToAttrib {
        r[at] = rI[i]
    }
    if err != nil {
        return nil, err
    }
    for _, at := range msp.RowToAttrib {
        // find the correct pubkey
        foundPK := false
        for _, pk := range pks {
            if pk.EggToAlpha[at] != nil {
                // CAREFUL: negative numbers do not play well with ScalarMult
                signLambda := lambda[at].Cmp(big.NewInt(0))
                signOmega := omega[at].Cmp(big.NewInt(0))
                var tmpLambda *bn256.GT
                var tmpOmega *bn256.G2
                if signLambda >= 0 {
                    tmpLambda = new(bn256.GT).ScalarMult(a.gt, lambda[at])
                } else {
                    tmpLambda = new(bn256.GT).ScalarMult(new(bn256.GT).Neg(a.gt), new(big.Int).Abs(lambda[at]))
                }
                if signOmega >= 0 {
                    tmpOmega = new(bn256.G2).ScalarMult(a.g2, omega[at])
                } else {
                    tmpOmega = new(bn256.G2).ScalarMult(new(bn256.G2).Neg(a.g2), new(big.Int).Abs(omega[at]))
                }
                c1[at] = new(bn256.GT).Add(tmpLambda, new(bn256.GT).ScalarMult(pk.EggToAlpha[at], r[at]))
                c2[at] = new(bn256.G2).ScalarMult(a.g2, r[at])
                c3[at] = new(bn256.G2).Add(new(bn256.G2).ScalarMult(pk.GToY[at], r[at]), tmpOmega)
                foundPK = true
                break
            }
        }
        if foundPK == false {
            return nil, fmt.Errorf("attribute not found in any pubkey")
        }
    }
    return &MAABECipher{
        C0: c0,
        C1x: c1,
        C2x: c2,
        C3x: c3,
        Msp: msp,
        SymEnc: symEnc,
        Iv: iv,
    }, nil
}

// MAABEKey represents a key corresponding to an attribute possessed by an
// entity. They are issued by the relevant authorities and are used for
// decryption in a MAABE scheme.
type MAABEKey struct {
    Gid string
    Attrib string
    Key *bn256.G1
}

// GenerateAttribKeys generates a list of attribute keys for the given user
// (represented by its Global ID) that possesses the given list of attributes.
// In case of a failed procedure an error is returned. The relevant authority
// has to check that the entity actually possesses the attributes via some
// other channel.
func (auth *MAABEAuth) GenerateAttribKeys(gid string, attribs []string) ([]*MAABEKey, error) {
    // sanity checks
    if len(gid) == 0 {
        return nil, fmt.Errorf("GID cannot be empty")
    }
    if len(attribs) == 0 {
        return nil, fmt.Errorf("attribute cannot be empty")
    }
    if auth.Maabe == nil {
        return nil, fmt.Errorf("ma-abe scheme cannot be nil")
    }
    hash, err := bn256.HashG1(gid)
    if err != nil {
        return nil, err
    }
    ks := make([]*MAABEKey, len(attribs))
    for i, at := range attribs {
        var k *bn256.G1
        if auth.Sk.Alpha[at] != nil && auth.Sk.Y[at] != nil {
            k = new(bn256.G1).Add(new(bn256.G1).ScalarMult(auth.Maabe.g1, auth.Sk.Alpha[at]), new(bn256.G1).ScalarMult(hash, auth.Sk.Y[at]))
            ks[i] = &MAABEKey{
                Gid: gid,
                Attrib: at,
                Key: k,
            }
        } else {
            return nil, fmt.Errorf("attribute not found in secret key")
        }
    }
    return ks, nil
}

// Decrypt takes a ciphertext in a MAABE scheme and a set of attribute keys
// belonging to the same entity, and attempts to decrypt the cipher. This is
// possible only if the set of possessed attributes/keys suffices the
// decryption policy of the ciphertext. In case this is not possible or
// something goes wrong an error is returned.
func (a * MAABE) Decrypt(ct *MAABECipher, ks []*MAABEKey) (string, error) {
    // sanity checks
    if len(ks) == 0 {
        return "", fmt.Errorf("empty set of attribute keys")
    }
    gid := ks[0].Gid
    for _, k := range ks {
        if k.Gid != gid {
            return "", fmt.Errorf("not all GIDs are the same")
        }
    }
    // get hashed GID
    hash, err := bn256.HashG1(gid)
    if err != nil {
        return "", err
    }
    // find out which attributes are valid and extract them
    goodMatRows := make([]data.Vector, 0)
    goodAttribs := make([]string, 0)
    aToK := make(map[string]*MAABEKey)
    for _, k := range ks {
        aToK[k.Attrib] = k
    }
    for i, at := range ct.Msp.RowToAttrib {
        if aToK[at] != nil {
            goodMatRows = append(goodMatRows, ct.Msp.Mat[i])
            goodAttribs = append(goodAttribs, at)
        }
    }
    goodMat, err := data.NewMatrix(goodMatRows)
    if err != nil {
        return "", err
    }
    //choose consts c_x, such that \sum c_x A_x = (1,0,...,0)
    // if they don't exist, keys are not ok
    goodCols := goodMat.Cols()
    one := data.NewConstantVector(goodCols, big.NewInt(0))
    one[0] = big.NewInt(1)
    c, err := data.GaussianEliminationSolver(goodMat.Transpose(), one, a.P)
    if err != nil {
        return "", err
    }
    cx := make(map[string]*big.Int)
    for i, at := range goodAttribs {
        cx[at] = c[i]
    }
    // compute intermediate values
    eggLambda := make(map[string]*bn256.GT)
    for _, at := range goodAttribs {
        if ct.C1x[at] != nil && ct.C2x[at] != nil && ct.C3x[at] != nil {
            num := new(bn256.GT).Add(ct.C1x[at], bn256.Pair(hash, ct.C3x[at]))
            den := new(bn256.GT).Neg(bn256.Pair(aToK[at].Key, ct.C2x[at]))
            eggLambda[at] = new(bn256.GT).Add(num, den)
        } else {
            return "", fmt.Errorf("attribute %s not in ciphertext dicts", at)
        }
    }
    eggs := new(bn256.GT).ScalarBaseMult(big.NewInt(0))
    for _, at := range goodAttribs {
        if eggLambda[at] != nil {
            sign := cx[at].Cmp(big.NewInt(0))
            if sign == 1 {
                eggs.Add(eggs, new(bn256.GT).ScalarMult(eggLambda[at], cx[at]))
            } else if sign == -1 {
                eggs.Add(eggs, new(bn256.GT).ScalarMult(new(bn256.GT).Neg(eggLambda[at]), new(big.Int).Abs(cx[at])))
            }
        } else {
            return "", fmt.Errorf("missing intermediate result")
        }
    }
    // calculate key for symmetric encryption
    symKey := new(bn256.GT).Add(ct.C0, new(bn256.GT).Neg(eggs))
    // now decrypt message with it
    keyCBC := sha256.Sum256([]byte(symKey.String()))
    cipherAES, err := aes.NewCipher(keyCBC[:])
    if err != nil {
        return "", err
    }
    msgPad := make([]byte, len(ct.SymEnc))
    decrypter := cbc.NewCBCDecrypter(cipherAES, ct.Iv)
    decrypter.CryptBlocks(msgPad, ct.SymEnc)
    // unpad the message
    padLen := int(msgPad[len(msgPad)-1])
    if (len(msgPad) - padLen) < 0 {
        return "", fmt.Errorf("failed to decrypt")
    }
    msgByte := msgPad[0:(len(msgPad) - padLen)]
    return string(msgByte), nil
}
