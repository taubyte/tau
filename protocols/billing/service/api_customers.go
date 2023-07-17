package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	cr "bitbucket.org/taubyte/p2p/streams/command/response"
	"github.com/taubyte/utils/maps"

	stripeCommon "github.com/stripe/stripe-go/v72"
	"github.com/stripe/stripe-go/v72/paymentmethod"

	stripeCustomer "github.com/stripe/stripe-go/v72/customer"

	idutils "github.com/taubyte/utils/id"

	"github.com/taubyte/go-interfaces/p2p/streams"
	authIface "github.com/taubyte/go-interfaces/services/auth"
)

func init() {
	initStripe()
}

func (cs *customersService) serviceHandler(ctx context.Context, conn streams.Connection, body streams.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, err
	}

	switch action {
	case "list":
		return cs.listHandler(ctx)
	case "new":
		project, err := maps.String(body, "project")
		if err != nil {
			return nil, err
		}

		provider, err := maps.String(body, "provider")
		if err != nil {
			return nil, err
		}
		return cs.new(ctx, project, provider)
	default:
		return nil, errors.New("Customers action `" + action + "` not reconized.")
	}
}

func (cs *customersService) listHandler(ctx context.Context) (cr.Response, error) {
	ids := make([]string, 0)
	unique := make(map[string]bool)
	_ids, err := cs.billing.db.List(ctx, "/customers/")
	if err != nil {
		return nil, err
	}

	if len(_ids) == 0 {
		return nil, fmt.Errorf("No customers are registered")
	}

	for _, id := range _ids {
		list := strings.Split(id, "/")
		if len(list) > 1 {
			if _, ok := unique[list[2]]; !ok {
				unique[list[2]] = true
				ids = append(ids, list[2])
			}
		}
	}

	return cr.Response{"ids": ids}, nil
}

func (cs *customersService) new(ctx context.Context, project string, provider string) (cr.Response, error) {
	switch provider {
	case "stripe":
		return cs.newStripe(ctx, project)
	default:
		return nil, fmt.Errorf("Unsupported payment platform `%s`", provider)
	}
}

func (cs *customersService) customer(ctx context.Context, customerId string) (*customer, error) {
	customerKeyPrefix := fmt.Sprintf("/customers/%s/", customerId)
	ret, err := cs.billing.db.List(ctx, customerKeyPrefix)
	if err != nil || len(ret) == 0 {
		return nil, fmt.Errorf("Customer %s does not exist!", customerId)
	}

	return &customer{
		parent: cs,
		id:     customerId,
	}, nil
}

func (c *customer) listPaymentMethods(ctx context.Context, provider string) (cr.Response, error) {
	switch provider {
	case "stripe":
		return c.listStripePaymentMethods(ctx)
	default:
		return nil, fmt.Errorf("Unsupported payment platform `%s`", provider)
	}
}

func (c *customer) listStripePaymentMethods(ctx context.Context) (cr.Response, error) {
	stripeKey := fmt.Sprintf("/customers/%s/stripe", c.id)
	stripeCustomerIdBytes, err := c.parent.billing.db.Get(ctx, stripeKey)
	if err != nil {
		return nil, fmt.Errorf("Strip not supported for customer %s!", c.id)
	}

	params := &stripeCommon.PaymentMethodListParams{
		Customer: stripeCommon.String(
			string(stripeCustomerIdBytes),
		),
		Type: stripeCommon.String("card"),
	}
	i := paymentmethod.List(
		params,
	)
	payments := make([]*stripeCommon.PaymentMethod, 0)
	for i.Next() {
		payments = append(payments, i.PaymentMethod())
	}

	return cr.Response{
		"stripe": payments,
	}, nil
}

func (c *customer) addPaymentMethod(ctx context.Context, provider string, token string) (cr.Response, error) {
	switch provider {
	case "stripe":
		return c.addStripePaymentMethod(ctx, token)
	default:
		return nil, fmt.Errorf("Unsupported payment platform `%s`", provider)
	}
}

func (c *customer) addStripePaymentMethod(ctx context.Context, token string) (cr.Response, error) {
	stripeKey := fmt.Sprintf("/customers/%s/stripe", c.id)
	stripeCustomerIdBytes, err := c.parent.billing.db.Get(ctx, stripeKey)
	if err != nil {
		return nil, fmt.Errorf("Strip not supported for customer %s!", c.id)
	}

	params := &stripeCommon.PaymentMethodParams{
		Card: &stripeCommon.PaymentMethodCardParams{
			Token: stripeCommon.String(token),
		},
		Type: stripeCommon.String("card"),
	}

	pm, err := paymentmethod.New(params)
	if err != nil {
		return nil, fmt.Errorf("Failed to add payment to %s (%s) with %w", c.id, string(stripeCustomerIdBytes), err)
	}

	attachParams := &stripeCommon.PaymentMethodAttachParams{
		Customer: stripeCommon.String(
			string(stripeCustomerIdBytes),
		),
	}

	pm, err = paymentmethod.Attach(
		pm.ID,
		attachParams,
	)
	if err != nil {
		return nil, fmt.Errorf("Failed to attach payment to %s (%s) with %w", c.id, string(stripeCustomerIdBytes), err)
	}

	return cr.Response{
		"return": pm,
	}, nil
}

func (cs *customersService) fetch(ctx context.Context, project_id string) (cr.Response, error) {
	prjKey := fmt.Sprintf("/projects/%s", project_id)
	customerIdBytes, err := cs.billing.db.Get(ctx, prjKey)
	if err != nil {
		return nil, fmt.Errorf("No customer found for project. Failed with %w", err)
	}

	providers := make([]string, 0)
	customerId := string(customerIdBytes)
	for _, provider := range []string{"stripe"} {
		customerKey := fmt.Sprintf("/customers/%s/%s", customerId, provider)
		_, err := cs.billing.db.Get(ctx, customerKey)
		if err != nil {
			continue
		}
		providers = append(providers, provider)
	}

	return cr.Response{
		"id":        customerId,
		"providers": providers,
	}, nil
}

func initStripe() {
	stripeCommon.Key = "sk_live_XDWnfeO2SGypICpQlU6F7C7H004xQrz4u6"
}

func (cs *customersService) newStripe(ctx context.Context, project string) (cr.Response, error) {
	_project := cs.billing.authClient.Projects().Get(project)

	if _project == nil {
		return nil, fmt.Errorf("Project (id=`%s`) does not exist!", project)
	}

	if _project.Id != project {
		return nil, fmt.Errorf("Failed to fetch project (id=`%s`)!", project)
	}

	_project = &authIface.Project{
		Id:   project,
		Name: "Project " + _project.Name,
	}

	params := &stripeCommon.CustomerParams{
		Name:        stripeCommon.String(_project.Id),
		Description: stripeCommon.String(fmt.Sprintf("%s/name=%s", _project.Id, _project.Name)),
	}

	customer, err := stripeCustomer.New(params)
	if err != nil {
		return nil, err
	}

	prjKey := fmt.Sprintf("/projects/%s", project)
	_, err = cs.billing.db.Get(ctx, prjKey)
	if err == nil {
		return nil, errors.New("Project customer exists!")
	}

	customerID := idutils.Generate(project)
	key := fmt.Sprintf("/customers/%s/stripe", customerID)

	err = cs.billing.db.Put(ctx, key, []byte(customer.ID))
	if err != nil {
		return nil, err
	}

	err = cs.billing.db.Put(ctx, prjKey, []byte(customerID))
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"id":       customerID,
		"provider": "stripe",
	}, nil
}

func (c *customer) deletePaymentMethod(ctx context.Context, provider, fingerprint string) (cr.Response, error) {
	switch provider {
	case "stripe":
		return c.deleteStripePayment(ctx, fingerprint)
	default:
		return nil, fmt.Errorf("Unsupported payment platform `%s`", provider)
	}
}

func (c *customer) deleteStripePayment(ctx context.Context, fingerprint string) (cr.Response, error) {
	stripeKey := fmt.Sprintf("/customers/%s/stripe", c.id)
	stripeCustomerIdBytes, err := c.parent.billing.db.Get(ctx, stripeKey)
	if err != nil {
		return nil, fmt.Errorf("Stripe not supported for customer %s!", c.id)
	}

	params := &stripeCommon.PaymentMethodListParams{
		Customer: stripeCommon.String(
			string(stripeCustomerIdBytes),
		),
		Type: stripeCommon.String("card"),
	}
	i := paymentmethod.List(
		params,
	)
	var pid string
	for i.Next() {
		if fingerprint == i.PaymentMethod().Card.Fingerprint {
			pid = i.PaymentMethod().ID
			break
		}
	}
	if len(pid) == 0 {
		return nil, fmt.Errorf("Payment with fingerPrint %s not found", fingerprint)
	}
	pm, _ := paymentmethod.Detach(
		pid,
		nil,
	)
	return cr.Response{
		"deletedPayment": pm,
	}, nil
}
