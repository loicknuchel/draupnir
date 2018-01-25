package routes

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gocardless/draupnir/auth"
	"github.com/gocardless/draupnir/models"
	"github.com/google/jsonapi"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
)

func decodeJSON(r io.Reader, out interface{}) {
	err := json.NewDecoder(r).Decode(out)
	if err != nil {
		log.Panic(err)
	}
}

func TestGetImage(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/images/1", nil)

	store := FakeImageStore{
		_Get: func(id int) (models.Image, error) {
			return models.Image{
				ID:         1,
				BackedUpAt: timestamp(),
				Ready:      false,
				CreatedAt:  timestamp(),
				UpdatedAt:  timestamp(),
			}, nil
		},
	}

	routeSet := Images{ImageStore: store, Authenticator: AllowAll{}}
	router := mux.NewRouter()
	router.HandleFunc("/images/{id}", routeSet.Get)
	router.ServeHTTP(recorder, req)

	var response jsonapi.OnePayload
	decodeJSON(recorder.Body, &response)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, getImageFixture, response)
}

func TestGetImageWhenAuthenticationFails(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/images/1", nil)

	authenticator := FakeAuthenticator{
		_AuthenticateRequest: func(r *http.Request) (string, error) {
			return "", errors.New("Invalid email address")
		},
	}

	logger, output := NewFakeLogger()
	routeSet := Images{Authenticator: authenticator, Logger: logger}

	handler := http.HandlerFunc(routeSet.Get)
	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	assert.Contains(t, output.String(), "Invalid email address")
}

func TestListImages(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/images", nil)

	store := FakeImageStore{
		_List: func() ([]models.Image, error) {
			return []models.Image{
				models.Image{
					ID:         1,
					BackedUpAt: timestamp(),
					Ready:      false,
					CreatedAt:  timestamp(),
					UpdatedAt:  timestamp(),
				},
			}, nil
		},
	}

	handler := Images{ImageStore: store, Authenticator: AllowAll{}}.List
	handler(recorder, req)

	var response jsonapi.ManyPayload
	decodeJSON(recorder.Body, &response)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, response, listImagesFixture)
}

func TestCreateImage(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := createImageRequest{
		BackedUpAt: timestamp(),
		Anon:       "SELECT * FROM foo;",
	}
	body := bytes.NewBuffer([]byte{})
	jsonapi.MarshalOnePayload(body, &request)

	req := httptest.NewRequest("POST", "/images", body)

	executor := FakeExecutor{
		_CreateBtrfsSubvolume: func(id int) error { assert.Equal(t, id, 1); return nil },
	}

	store := FakeImageStore{
		_Create: func(image models.Image) (models.Image, error) {
			assert.Equal(t, image.Anon, "SELECT * FROM foo;")
			return models.Image{
				ID:         1,
				BackedUpAt: image.BackedUpAt,
				Ready:      false,
				CreatedAt:  timestamp(),
				UpdatedAt:  timestamp(),
			}, nil
		},
	}

	routeSet := Images{ImageStore: store, Executor: executor, Authenticator: AllowAll{}}
	routeSet.Create(recorder, req)

	var response jsonapi.OnePayload
	decodeJSON(recorder.Body, &response)

	assert.Equal(t, http.StatusCreated, recorder.Code)
	assert.Equal(t, createImageFixture, response)
}

func TestImageCreateReturnsErrorWithInvalidPayload(t *testing.T) {
	recorder := httptest.NewRecorder()
	body := `{"this is": "not a valid JSON API request payload"}`
	req := httptest.NewRequest("POST", "/images", strings.NewReader(body))

	logger, output := NewFakeLogger()
	routeSet := Images{Authenticator: AllowAll{}, Logger: logger}
	routeSet.Create(recorder, req)

	var response APIError
	decodeJSON(recorder.Body, &response)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Equal(t, invalidJSONError, response)
	assert.Contains(t, output.String(), "data is not a jsonapi representation")
}

func TestImageCreateReturnsErrorWhenSubvolumeCreationFails(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := createImageRequest{
		BackedUpAt: timestamp(),
		Anon:       "SELECT * FROM foo;",
	}
	body := bytes.NewBuffer([]byte{})
	jsonapi.MarshalOnePayload(body, &request)
	req := httptest.NewRequest("POST", "/images", body)

	store := FakeImageStore{
		_Create: func(image models.Image) (models.Image, error) {
			return models.Image{
				ID:         1,
				BackedUpAt: timestamp(),
				Ready:      false,
				CreatedAt:  timestamp(),
				UpdatedAt:  timestamp(),
			}, nil
		},
	}

	executor := FakeExecutor{
		_CreateBtrfsSubvolume: func(id int) error {
			return errors.New("some btrfs error")
		},
	}
	logger, output := NewFakeLogger()

	routeSet := Images{
		ImageStore:    store,
		Executor:      executor,
		Authenticator: AllowAll{},
		Logger:        logger,
	}
	routeSet.Create(recorder, req)

	var response APIError
	decodeJSON(recorder.Body, &response)

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.Equal(t, internalServerError, response)
	assert.Contains(t, output.String(), "error=\"some btrfs error\"")
}

func TestImageDone(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/images/1/done", nil)

	image := models.Image{
		ID:         1,
		BackedUpAt: timestamp(),
		Ready:      false,
		CreatedAt:  timestamp(),
		UpdatedAt:  timestamp(),
	}

	store := FakeImageStore{
		_Get: func(id int) (models.Image, error) {
			assert.Equal(t, 1, id)

			return image, nil
		},
		_MarkAsReady: func(i models.Image) (models.Image, error) {
			assert.Equal(t, image, i)

			i.Ready = true
			return i, nil
		},
	}

	executor := FakeExecutor{
		_FinaliseImage: func(i models.Image) error {
			assert.Equal(t, image, i)

			return nil
		},
	}

	routeSet := Images{ImageStore: store, Executor: executor, Authenticator: AllowAll{}}
	router := mux.NewRouter()
	router.HandleFunc("/images/{id}/done", routeSet.Done)
	router.ServeHTTP(recorder, req)

	var response jsonapi.OnePayload
	decodeJSON(recorder.Body, &response)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, doneImageFixture, response)
}

func TestImageDoneWithNonNumericID(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/images/bad_id/done", nil)

	logger, output := NewFakeLogger()

	routeSet := Images{Authenticator: AllowAll{}, Logger: logger}
	router := mux.NewRouter()
	router.HandleFunc("/images/{id}/done", routeSet.Done)
	router.ServeHTTP(recorder, req)

	var response APIError
	decodeJSON(recorder.Body, &response)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.Equal(t, notFoundError, response)
	assert.Contains(t, output.String(), "invalid syntax")
}

func TestImageDestroy(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/images/1", nil)

	image := models.Image{
		ID:         1,
		BackedUpAt: timestamp(),
		Ready:      false,
		CreatedAt:  timestamp(),
		UpdatedAt:  timestamp(),
	}

	store := FakeImageStore{
		_Get: func(id int) (models.Image, error) {
			assert.Equal(t, 1, id)

			return image, nil
		},
		_Destroy: func(i models.Image) error {
			assert.Equal(t, image, i)
			return nil
		},
	}

	executor := FakeExecutor{
		_DestroyImage: func(imageID int) error {
			assert.Equal(t, 1, imageID)
			return nil
		},
	}

	logger, output := NewFakeLogger()

	router := mux.NewRouter()
	routeSet := Images{ImageStore: store, Executor: executor, Authenticator: AllowAll{}, Logger: logger}
	router.HandleFunc("/images/{id}", routeSet.Destroy).Methods("DELETE")
	router.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, 0, len(recorder.Body.Bytes()))
	assert.Contains(t, output.String(), "destroying image")
}

func TestImageDestroyFromUploadUser(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/images/1", nil)

	image := models.Image{
		ID:         1,
		BackedUpAt: timestamp(),
		Ready:      false,
		CreatedAt:  timestamp(),
		UpdatedAt:  timestamp(),
	}

	imageStore := FakeImageStore{
		_Get: func(id int) (models.Image, error) {
			assert.Equal(t, 1, id)
			return image, nil
		},
		_Destroy: func(i models.Image) error {
			assert.Equal(t, image, i)
			return nil
		},
	}

	destroyedImages := make([]int, 0)

	instanceStore := FakeInstanceStore{
		_List: func() ([]models.Instance, error) {
			return []models.Instance{
				models.Instance{ID: 1, ImageID: 1},
				models.Instance{ID: 2, ImageID: 2},
				models.Instance{ID: 3, ImageID: 1},
			}, nil
		},
		_Destroy: func(instance models.Instance) error {
			destroyedImages = append(destroyedImages, instance.ID)
			return nil
		},
	}

	executor := FakeExecutor{
		_DestroyImage: func(imageID int) error {
			assert.Equal(t, 1, imageID)
			return nil
		},
		_DestroyInstance: func(id int) error {
			return nil
		},
	}

	authenticator := FakeAuthenticator{
		_AuthenticateRequest: func(r *http.Request) (string, error) {
			return auth.UPLOAD_USER_EMAIL, nil
		},
	}

	logger, output := NewFakeLogger()

	router := mux.NewRouter()
	routeSet := Images{
		ImageStore:    imageStore,
		InstanceStore: instanceStore,
		Executor:      executor,
		Authenticator: authenticator,
		Logger:        logger,
	}
	router.HandleFunc("/images/{id}", routeSet.Destroy).Methods("DELETE")
	router.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, 0, len(recorder.Body.Bytes()))
	assert.Equal(t, []int{1, 3}, destroyedImages)
	assert.Contains(t, output.String(), "destroying instance")
	assert.Contains(t, output.String(), "destroying image")
}

func timestamp() time.Time {
	loc, err := time.LoadLocation("UTC")
	if err != nil {
		panic(err.Error())
	}
	return time.Date(2016, 1, 1, 12, 33, 44, 567000000, loc)
}
