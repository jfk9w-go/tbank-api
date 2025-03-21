package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AlekSi/pointer"
	"github.com/caarlos0/env"
	"github.com/davecgh/go-spew/spew"
	"github.com/jfk9w-go/based"
	"github.com/pkg/errors"

	tbank "github.com/jfk9w-go/tbank-api"
)

type jsonSessionStorage struct {
	path string
}

func (s jsonSessionStorage) LoadSession(ctx context.Context, phone string) (*tbank.Session, error) {
	file, err := s.open(os.O_RDONLY)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}

		return nil, err
	}

	defer file.Close()
	contents := make(map[string]tbank.Session)
	if err := json.NewDecoder(file).Decode(&contents); err != nil {
		return nil, errors.Wrap(err, "decode json")
	}

	if session, ok := contents[phone]; ok {
		return &session, nil
	}

	return nil, nil
}

func (s jsonSessionStorage) UpdateSession(ctx context.Context, phone string, session *tbank.Session) error {
	file, err := s.open(os.O_RDWR | os.O_CREATE)
	if err != nil {
		return err
	}

	stat, err := file.Stat()
	if err != nil {
		return errors.Wrap(err, "stat")
	}

	contents := make(map[string]tbank.Session)
	if stat.Size() > 0 {
		if err := json.NewDecoder(file).Decode(&contents); err != nil {
			return errors.Wrap(err, "decode json")
		}
	}

	if session != nil {
		contents[phone] = *session
	} else {
		delete(contents, phone)
	}

	if err := file.Truncate(0); err != nil {
		return errors.Wrap(err, "truncate file")
	}

	if _, err := file.Seek(0, 0); err != nil {
		return errors.Wrap(err, "seek to the start of file")
	}

	if err := json.NewEncoder(file).Encode(&contents); err != nil {
		return errors.Wrap(err, "encode json")
	}

	return nil
}

func (s jsonSessionStorage) open(flag int) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(s.path), os.ModeDir); err != nil {
		return nil, errors.Wrap(err, "create parent directory")
	}

	file, err := os.OpenFile(s.path, flag, 0644)
	if err != nil {
		return nil, errors.Wrap(err, "open file")
	}

	return file, nil
}

type authorizer struct{}

func (a authorizer) GetConfirmationCode(ctx context.Context, phone string) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Enter confirmation code for %s: ", phone)
	text, err := reader.ReadString('\n')
	if err != nil {
		return "", errors.Wrap(err, "read line from stdin")
	}

	return strings.Trim(text, " \n\t\v"), nil
}

type httpTransport struct {
	client http.Client
}

func (t *httpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	reqData, err := httputil.DumpRequestOut(req, true)
	if err != nil {
		return nil, errors.Wrap(err, "dump request")
	}

	fmt.Println(string(reqData))

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}

	respData, err := httputil.DumpResponse(resp, true)
	if err != nil {
		return nil, errors.Wrap(err, "dump response")
	}

	fmt.Println(string(respData))

	return resp, nil
}

func main() {
	var config struct {
		Phone        string `env:"TBANK_PHONE,required"`
		Password     string `env:"TBANK_PASSWORD,required"`
		SessionsFile string `env:"TBANK_SESSIONS_FILE,required"`
	}

	if err := env.Parse(&config); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := tbank.NewClient(tbank.ClientParams{
		Clock: based.StandardClock,
		Credential: tbank.Credential{
			Phone:    config.Phone,
			Password: config.Password,
		},
		SessionStorage: jsonSessionStorage{path: config.SessionsFile},
		Transport:      new(httpTransport),
		AuthFlow:       new(tbank.SeleniumAuthFlow),
	})

	if err != nil {
		panic(err)
	}

	ctx = tbank.WithAuthorizer(ctx, authorizer{})

	investOperationTypes, err := client.InvestOperationTypes(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Printf("found %d invest operation types\n", len(investOperationTypes.OperationsTypes))
	for _, operationType := range investOperationTypes.OperationsTypes {
		spew.Dump(operationType)
		break
	}

	investAccounts, err := client.InvestAccounts(ctx, &tbank.InvestAccountsIn{
		Currency: "RUB",
	})

	if err != nil {
		panic(err)
	}

	fmt.Printf("found %d invest accounts\n", investAccounts.Accounts.Count)
	for _, account := range investAccounts.Accounts.List {
		spew.Dump(account)

		investOperations, err := client.InvestOperations(ctx, &tbank.InvestOperationsIn{
			From:            time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
			To:              time.Now(),
			Limit:           10,
			BrokerAccountId: account.BrokerAccountId,
		})

		if err != nil {
			panic(err)
		}

		fmt.Printf("found %d invest operations in invest account '%s'\n", len(investOperations.Items), account.Name)
		for _, operation := range investOperations.Items {
			spew.Dump(operation)
			break
		}

		break //nolint:staticcheck
	}

	accounts, err := client.AccountsLightIb(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Printf("found %d accounts\n", len(accounts))
	if len(accounts) == 0 {
		return
	}

	for _, account := range accounts {
		spew.Dump(account)

		if account.AccountType == "Telecom" || account.AccountType == "ExternalAccount" {
			continue
		}

		operations, err := client.Operations(ctx, &tbank.OperationsIn{
			Account: account.Id,
			Start:   time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
			//End:     pointer.To(time.Now()),
		})

		if err != nil {
			panic(err)
		}

		fmt.Printf("found %d operations in account '%s'\n", len(operations), account.Name)
		if len(operations) == 0 {
			return
		}

		for _, operation := range operations {
			if pointer.Get(operation.HasShoppingReceipt) {
				spew.Dump(operation)

				receipt, err := client.ShoppingReceipt(ctx, &tbank.ShoppingReceiptIn{
					OperationId: operation.Id,
				})

				switch {
				case errors.Is(err, tbank.ErrNoDataFound):
					continue
				case err != nil:
					panic(err)
				}

				for _, item := range receipt.Receipt.Items {
					fmt.Println(item.Name)
				}

				spew.Dump(receipt)

				break
			}
		}
	}

	clientOfferEssences, err := client.ClientOfferEssences(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Printf("found %d client offer essences\n", len(clientOfferEssences))
	spew.Dump(clientOfferEssences)
}
