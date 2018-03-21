// -*- Mode: Go; indent-tabs-mode: t -*-

/*
 * Copyright (C) 2017-2018 Canonical Ltd
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License version 3 as
 * published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package assertion

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/CanonicalLtd/serial-vault/account"
	"github.com/CanonicalLtd/serial-vault/datastore"
	"github.com/CanonicalLtd/serial-vault/service/log"
	"github.com/CanonicalLtd/serial-vault/service/response"
	"github.com/snapcore/snapd/asserts"
)

func modelAssertionHandler(w http.ResponseWriter, apiKey string, request ModelAssertionRequest) response.ErrorResponse {
	// Check that the reseller functionality is enabled for the brand
	acc, err := datastore.Environ.DB.GetAccount(request.BrandID)
	if err != nil {
		return response.ErrorResponse{Success: false, Code: "error-account", Message: err.Error(), StatusCode: http.StatusBadRequest}
	}
	if !acc.ResellerAPI {
		return response.ErrorResponse{Success: false, Code: "error-auth", Message: "This feature is not enabled for this account", StatusCode: http.StatusBadRequest}
	}

	// Validate the model by checking that it exists on the database
	model, err := datastore.Environ.DB.FindModel(request.BrandID, request.Name, apiKey)
	if err != nil {
		log.Message("MODEL", "invalid-model", "Cannot find model with the matching brand and model")
		return response.ErrorInvalidModel
	}

	assertions := []asserts.Assertion{}

	// Build the model assertion headers
	assertionHeaders, keypair, err := CreateModelAssertionHeaders(model)
	if err != nil {
		log.Message("MODEL", "create-assertion", err.Error())
		return response.ErrorCreateModelAssertion
	}

	// Sign the assertion with the snapd assertions module
	signedAssertion, err := datastore.Environ.KeypairDB.SignAssertion(asserts.ModelType, assertionHeaders, []byte(""), model.BrandID, keypair.KeyID, keypair.SealedKey)
	if err != nil {
		log.Message("MODEL", "signing-assertion", err.Error())
		return response.ErrorResponse{Success: false, Code: "signing-assertion", Message: err.Error(), StatusCode: http.StatusBadRequest}
	}

	// Add the account assertion to the assertions list
	fetchAssertionFromStore(&assertions, asserts.AccountType, []string{model.BrandID})

	// Add the account-key assertion to the assertions list
	fetchAssertionFromStore(&assertions, asserts.AccountKeyType, []string{keypair.KeyID})

	// Add the model assertion after the account and account-key assertions
	assertions = append(assertions, signedAssertion)

	// Return successful response with the signed assertions
	formatAssertionResponse(assertions, w)
	return response.ErrorResponse{Success: true}
}

// CreateModelAssertionHeaders returns the model assertion headers for a model
func CreateModelAssertionHeaders(m datastore.Model) (map[string]interface{}, datastore.Keypair, error) {

	// Get the assertion headers for the model
	assert, err := datastore.Environ.DB.GetModelAssert(m.ID)
	if err != nil {
		return nil, datastore.Keypair{}, err
	}

	// Get the keypair for the model assertion
	keypair, err := datastore.Environ.DB.GetKeypair(assert.KeypairID)
	if err != nil {
		return nil, keypair, err
	}

	// Create the model assertion header
	headers := map[string]interface{}{
		"type":              asserts.ModelType.Name,
		"authority-id":      m.BrandID,
		"brand-id":          m.BrandID,
		"series":            fmt.Sprintf("%d", assert.Series),
		"model":             m.Name,
		"architecture":      assert.Architecture,
		"store":             assert.Store,
		"gadget":            assert.Gadget,
		"kernel":            assert.Kernel,
		"sign-key-sha3-384": keypair.KeyID,
		"timestamp":         time.Now().Format(time.RFC3339),
	}

	// Check if the optional required-snaps field is needed
	if len(assert.RequiredSnaps) == 0 {
		return headers, keypair, nil
	}

	snapList := strings.Split(assert.RequiredSnaps, ",")
	reqdSnaps := []interface{}{}
	for _, s := range snapList {
		reqdSnaps = append(reqdSnaps, strings.TrimSpace(s))
	}
	headers["required-snaps"] = reqdSnaps

	return headers, keypair, nil
}

func fetchAssertionFromStore(assertions *[]asserts.Assertion, modelType *asserts.AssertionType, headers []string) {
	assertion, err := account.FetchAssertionFromStore(modelType, headers)
	if err != nil {
		log.Message("MODEL", "assertion", err.Error())
	} else {
		*assertions = append(*assertions, assertion)
	}
}

func formatAssertionResponse(assertions []asserts.Assertion, w http.ResponseWriter) error {
	w.Header().Set("Content-Type", asserts.MediaType)
	w.WriteHeader(http.StatusOK)
	encoder := asserts.NewEncoder(w)

	for _, assert := range assertions {
		err := encoder.Encode(assert)
		if err != nil {
			// Not much we can do if we're here - apart from panic!
			log.Message("MODEL", "assertion", "Error encoding the assertions.")
			return err
		}
	}

	return nil
}