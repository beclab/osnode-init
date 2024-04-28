package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/emicklei/go-restful/v3"
	"github.com/go-resty/resty/v2"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
)

type Header struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Response struct {
	Header

	Data any `json:"data,omitempty"` // data field, optional, object or list
}

type ProxyRequest struct {
	Op       string      `json:"op"`
	DataType string      `json:"datatype"`
	Version  string      `json:"version"`
	Group    string      `json:"group"`
	Param    interface{} `json:"param,omitempty"`
	Data     string      `json:"data,omitempty"`
	Token    string
}

type AccountResponse struct {
	Header
	Data *AccountResponseData `json:"data,omitempty"`
}

type AccountResponseData struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type AccountValue struct {
	Email   string `json:"email"`
	Userid  string `json:"userid"`
	Token   string `json:"token"`
	Expired any    `json:"expired"`
}

type AWSAccount struct {
	Cloud      string `json:"cloud"`      // "AWS",
	Bucket     string `json:"bucket"`     // "terminus-us-west-1",
	Token      string `json:"st"`         // "session token",
	Prefix     string `json:"prefix"`     // "fbcf5f573ed242c28758-342957450633",
	Secret     string `json:"sk"`         // "secret",
	Key        string `json:"ak"`         // "ASIAVJDTX4VST3ZPDMUV",
	Expiration string `json:"expiration"` // "1705550635000",
	Region     string `json:"region"`     // "us-west-1"
}

type AWSAccountResponse struct {
	Header
	Data *AWSAccount `json:"data"`
}

const (
	LABEL_CLUSTER_ID    = "bytetrade.io/cluster-id"
	LABEL_ACCESS_KEY    = "bytetrade.io/s3-ak"
	LABEL_SECRET_KEY    = "bytetrade.io/s3-sk"
	LABEL_SESSION_TOKEN = "bytetrade.io/s3-sts"
)

var (
	gvr = schema.GroupVersionResource{
		Group:    "sys.bytetrade.io",
		Version:  "v1alpha1",
		Resource: "terminus",
	}
)

func getAccountFromSettings(admin string) (userid, token string, err error) {
	settingsUrl := fmt.Sprintf("http://settings-service.user-space-%s/api/account", admin)
	client := resty.New().SetTimeout(2 * time.Second)

	req := &ProxyRequest{
		Op:       "getAccount",
		DataType: "account",
		Version:  "v1",
		Group:    "service.settings",
		Data:     "settings-account-space",
	}

	terminusNonce, err := GenTerminusNonce()
	if err != nil {
		klog.Error("generate nonce error, ", err)
		return
	}

	klog.Info("fetch account from settings, ", settingsUrl)
	resp, err := client.R().SetDebug(true).
		SetHeader(restful.HEADER_ContentType, restful.MIME_JSON).
		SetHeader("Terminus-Nonce", terminusNonce).
		SetBody(req).
		SetResult(&AccountResponse{}).
		Post(settingsUrl)

	if err != nil {
		klog.Error("request settings account api error, ", err)
		return
	}

	if resp.StatusCode() != http.StatusOK {
		klog.Error("request settings account api response not ok, ", resp.StatusCode())
		err = fmt.Errorf("settings account response, %d, %s", resp.StatusCode(), string(resp.Body()))
		return
	}

	accountResp := resp.Result().(*AccountResponse)
	klog.Infof("settings account api response, %+v", accountResp)
	if accountResp.Code != 0 {
		klog.Error("request settings account api response error, ", accountResp.Code, ", ", accountResp.Message)
		err = errors.New(accountResp.Message)
		return
	}

	if accountResp.Data == nil {
		klog.Error("request settings account api response data is nil, ", accountResp.Code, ", ", accountResp.Message)
		err = errors.New("request settings account api response data is nil")
		return
	}

	var value AccountValue
	err = json.Unmarshal([]byte(accountResp.Data.Value), &value)
	if err != nil {
		klog.Error("parse value error, ", err, ", ", value)
		return
	}
	userid = value.Userid
	token = value.Token

	return
}

func GetAwsAccountFromCloud(ctx context.Context, client dynamic.Interface, bucket string) (*AWSAccount, error) {
	// cloudUrl := "https://cloud-dev-api.bttcdn.com/v1/resource/stsToken"
	cloudUrl := "https://cloud-api.bttcdn.com/v1/resource/stsToken/setup"

	clusterId, ak, sk, st, err := getClusterId(ctx, client)
	if err != nil {
		return nil, err
	}

	httpClient := resty.New().SetTimeout(15 * time.Second).SetDebug(true)
	duration := 12 * time.Hour
	resp, err := httpClient.R().
		SetFormData(map[string]string{
			"clusterId":       clusterId,
			"ak":              ak,
			"sk":              sk,
			"st":              st,
			"bucket":          bucket,
			"bucketPrefix":    clusterId,
			"durationSeconds": fmt.Sprintf("%.0f", duration.Seconds()),
		}).
		SetResult(&AWSAccountResponse{}).
		Post(cloudUrl)

	if err != nil {
		klog.Error("fetch data from cloud error, ", err, ", ", cloudUrl)
		return nil, err
	}

	if resp.StatusCode() != http.StatusOK {
		klog.Error("fetch data from cloud response error, ", resp.StatusCode(), ", ", resp.Body())
		return nil, errors.New(string(resp.Body()))
	}

	awsResp := resp.Result().(*AWSAccountResponse)
	if awsResp.Code != http.StatusOK {
		klog.Error("get aws account from cloud error, ", awsResp.Code, ", ", awsResp.Message)
		return nil, errors.New(awsResp.Message)
	}
	klog.Infof("get aws account from cloud %v", ToJSON(awsResp))

	if awsResp.Data == nil {
		klog.Error("get aws account from cloud data is empty, ", awsResp.Code, ", ", awsResp.Message)
		return nil, errors.New("data is empty")
	}

	return awsResp.Data, nil
}

func getClusterId(ctx context.Context, client dynamic.Interface) (cluster_id, ak, sk, st string, err error) {

	data, err := client.Resource(gvr).Get(ctx, "terminus", metav1.GetOptions{})
	if err != nil {
		klog.Error("get terminus define error, ", err)
		return
	}

	labels := data.GetLabels()
	var ok = false
	if labels != nil {
		if cluster_id, ok = labels[LABEL_CLUSTER_ID]; ok {
			klog.Info("found cluster id, ", cluster_id)
		}
	}

	if cluster_id == "" {
		klog.Error("cluster id not found")
		err = errors.New("cluster id not found")
		return
	}

	annotations := data.GetAnnotations()
	if annotations != nil {
		if ak, ok = annotations[LABEL_ACCESS_KEY]; ok {
			klog.Info("found access key, ", ak)
		}

		if sk, ok = annotations[LABEL_SECRET_KEY]; ok {
			klog.Info("found secret key, ", sk)
		}

		if st, ok = annotations[LABEL_SESSION_TOKEN]; ok {
			klog.Info("found session token, ", st)
		}

		return
	}

	return
}

func updateAwsAccount(ctx context.Context, client dynamic.Interface, account *AWSAccount) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		data, err := client.Resource(gvr).Get(ctx, "terminus", metav1.GetOptions{})
		if err != nil {
			klog.Error("get terminus define error, ", err)
			return err
		}
		annotations := data.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}

		annotations[LABEL_ACCESS_KEY] = account.Key
		annotations[LABEL_SECRET_KEY] = account.Secret
		annotations[LABEL_SESSION_TOKEN] = account.Token

		data.SetAnnotations(annotations)

		_, err = client.Resource(gvr).Update(ctx, data, metav1.UpdateOptions{})
		if err != nil {
			klog.Error("update terminus s3 lables error, ", err)
			return err
		}

		return nil
	})
}
