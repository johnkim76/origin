package servicebroker

import (
	"errors"
	"net/http"
	"reflect"
	"testing"

	templatev1 "github.com/openshift/api/template/v1"
	faketemplatev1 "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1/fake"
	templatelister "github.com/openshift/client-go/template/listers/template/v1"
	"github.com/openshift/origin/pkg/templateservicebroker/openservicebroker/api"

	authorizationv1 "k8s.io/api/authorization/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

type fakeTemplateLister struct{}

func (fakeTemplateLister) List(selector labels.Selector) ([]*templatev1.Template, error) {
	return nil, nil
}

func (fakeTemplateLister) Templates(namespace string) templatelister.TemplateNamespaceLister {
	return nil
}

func (fakeTemplateLister) GetByUID(uid string) (*templatev1.Template, error) {
	return &templatev1.Template{}, nil
}

var _ templatelister.TemplateLister = &fakeTemplateLister{}

func TestProvisionConflict(t *testing.T) {
	fakekc := &fake.Clientset{}
	fakekc.AddReactor("create", "subjectaccessreviews", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	})

	faketemplateclient := &faketemplatev1.FakeTemplateV1{Fake: &clienttesting.Fake{}}
	var conflict int
	faketemplateclient.AddReactor("update", "brokertemplateinstances", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if conflict > 0 {
			conflict--
			return true, nil, k8serrors.NewConflict(templatev1.Resource("brokertemplateinstance"), "", nil)
		}
		return true, &templatev1.BrokerTemplateInstance{}, nil
	})

	b := &Broker{
		templateclient:     faketemplateclient,
		kc:                 fakekc,
		lister:             &fakeTemplateLister{},
		templateNamespaces: map[string]struct{}{"": {}},
	}

	// after 5 conflicts we give up and return ConcurrencyError
	conflict = 5
	resp := b.Provision(&user.DefaultInfo{}, "", &api.ProvisionRequest{})
	if !reflect.DeepEqual(resp, api.NewResponse(http.StatusUnprocessableEntity, &api.ConcurrencyError, nil)) {
		t.Errorf("got response %#v, expected 422/ConcurrencyError", *resp)
	}

	// with fewer conflicts, we should get there in the end
	conflict = 4
	resp = b.Provision(&user.DefaultInfo{}, "", &api.ProvisionRequest{})
	if !reflect.DeepEqual(resp, api.NewResponse(http.StatusAccepted, api.ProvisionResponse{Operation: api.OperationProvisioning}, nil)) {
		t.Errorf("got response %#v, expected 202", *resp)
	}
}

func TestCustomLabels(t *testing.T) {
	b := &Broker{}
	fakeTemplate := templatev1.Template{}
	fakeTemplate.ObjectLabels = make(map[string]string)

	// empty Custom Label Parameter
	resp := b.ensureCustomLabel(&fakeTemplate, "")
	if resp != nil {
		t.Errorf("got response %#v, expected nil", *resp)
	}

	// valid label values
	resp = b.ensureCustomLabel(&fakeTemplate, "k1 = v1, k2, k3=v3")
	if resp != nil {
		t.Errorf("got response %#v, expected nil", *resp)
	}

	// valid label values
	resp = b.ensureCustomLabel(&fakeTemplate, "k1, k2=v2,")
	if resp != nil {
		t.Errorf("got response %#v, expected nil", *resp)
	}

	// invalid values
	resp = b.ensureCustomLabel(&fakeTemplate, "label1=L1*, label2=L2:")
	if !reflect.DeepEqual(resp, api.InvalidCustomLabelParameter(errors.New(invalidCustomLabelMsg))) {
		t.Errorf("got response %#v, expected 422/StatusUnprocessableEntity", *resp)
	}

	// invalid values (empty key string)
	resp = b.ensureCustomLabel(&fakeTemplate, "label1=L1, =L2")
	if !reflect.DeepEqual(resp, api.InvalidCustomLabelParameter(errors.New(missingLabelKeyMsg))) {
		t.Errorf("got response %#v, expected 422/StatusUnprocessableEntity", *resp)
	}

	// invalid values (spaces in key/value string)
	resp = b.ensureCustomLabel(&fakeTemplate, " label 1=L1, label2=L 2 ")
	if !reflect.DeepEqual(resp, api.InvalidCustomLabelParameter(errors.New(invalidKeyValueStrMsg))) {
		t.Errorf("got response %#v, expected 422/StatusUnprocessableEntity", *resp)
	}
}
