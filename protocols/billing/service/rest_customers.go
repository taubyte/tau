package service

import (
	"github.com/taubyte/go-interfaces/services/http"
)

func (srv *BillingService) deletePaymentForCustomerHTTPHandler(ctx http.Context) (interface{}, error) {

	_provider, err := ctx.GetStringVariable("provider")
	if err != nil {
		return nil, err
	}

	_customerId, err := ctx.GetStringVariable("id")
	if err != nil {
		return nil, err
	}

	_fingerPrint, err := ctx.GetStringVariable("fingerprint")
	if err != nil {
		return nil, err
	}

	customer, err := srv.customers.customer(ctx.Request().Context(), _customerId)
	if err != nil {
		return nil, err
	}

	_, err = customer.deletePaymentMethod(ctx.Request().Context(), _provider, _fingerPrint)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (srv *BillingService) listPaymentForCustomerHTTPHandler(ctx http.Context) (interface{}, error) {

	_provider, err := ctx.GetStringVariable("provider")
	if err != nil {
		return nil, err
	}

	_customerId, err := ctx.GetStringVariable("id")
	if err != nil {
		return nil, err
	}

	customer, err := srv.customers.customer(ctx.Request().Context(), _customerId)
	if err != nil {
		return nil, err
	}

	payments, err := customer.listPaymentMethods(ctx.Request().Context(), _provider)
	if err != nil {
		return nil, err
	}

	return payments, nil
}

func (srv *BillingService) newPaymentForCustomerHTTPHandler(ctx http.Context) (interface{}, error) {

	_provider, err := ctx.GetStringVariable("provider")
	if err != nil {
		return nil, err
	}

	_customerId, err := ctx.GetStringVariable("id")
	if err != nil {
		return nil, err
	}

	_token, err := ctx.GetStringVariable("token")
	if err != nil {
		return nil, err
	}

	customer, err := srv.customers.customer(ctx.Request().Context(), _customerId)
	if err != nil {
		return nil, err
	}

	_, err = customer.addPaymentMethod(ctx.Request().Context(), _provider, _token)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (srv *BillingService) newCustomerHTTPHandler(ctx http.Context) (interface{}, error) {

	_provider, err := ctx.GetStringVariable("provider")
	if err != nil {
		return nil, err
	}

	_project, err := ctx.GetStringVariable("project")
	if err != nil {
		return nil, err
	}

	resp, err := srv.customers.new(ctx.Request().Context(), _project, _provider)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (srv *BillingService) getCustomerHTTPHandler(ctx http.Context) (interface{}, error) {
	// TODO, we should have a fixture that pushes a fake customer instead of srv.sandbox
	if srv.sandbox == true {
		return map[string]interface{}{"id": "1", "providers": []string{"test"}}, nil
	}

	_project, err := ctx.GetStringVariable("id")
	if err != nil {
		return nil, err
	}

	resp, err := srv.customers.fetch(ctx.Request().Context(), _project)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (srv *BillingService) setupCustomersHTTPRoutes() {
	srv.http.GET(&http.RouteDefinition{
		Path: "/ping",
		Handler: func(ctx http.Context) (interface{}, error) {
			return map[string]string{"ping": "pong11"}, nil
		},
	})

	srv.http.POST(&http.RouteDefinition{
		Path:  "/customer/{provider}/new",
		Scope: []string{"customer/new"},
		Vars: http.Variables{
			Required: []string{"provider", "project"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.newCustomerHTTPHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Path:  "/project/{id}",
		Scope: []string{"customer/get"},
		Vars: http.Variables{
			Required: []string{"id"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.getCustomerHTTPHandler,
	})

	srv.http.POST(&http.RouteDefinition{
		Path:  "/customer/{id}/{provider}/payment",
		Scope: []string{"customer/payment/new"},
		Vars: http.Variables{
			Required: []string{"provider", "id", "token"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.newPaymentForCustomerHTTPHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Path:  "/customer/{id}/{provider}",
		Scope: []string{"customer/payment/read"},
		Vars: http.Variables{
			Required: []string{"provider", "id"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.listPaymentForCustomerHTTPHandler,
	})

	srv.http.DELETE(&http.RouteDefinition{
		Path:  "/customer/{id}/{provider}/payment",
		Scope: []string{"customer/payment/delete"},
		Vars: http.Variables{
			Required: []string{"provider", "id", "fingerprint"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.deletePaymentForCustomerHTTPHandler,
	})
}
