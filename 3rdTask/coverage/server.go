package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

var datasetPath = "dataset.xml"

type xmlUserRow struct {
	ID        int    `xml:id`
	FirstName string `xml:first_name`
	LastName  string `xml:last_name`
	Age       int    `xml:age`
	About     string `xml:about`
	Gender    string `xml:about`
}

type xmlDataset struct {
	Rows []xmlUserRow `xml:row`
}

func loadUsers(filename string) ([]User, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var root xmlDataset
	if err := xml.NewDecoder(file).Decode(&root); err != nil {
		return nil, err
	}

	users := make([]User, len(root.Rows))
	for i, row := range root.Rows {
		users[i] = User{
			ID:     row.ID,
			Name:   row.FirstName + " " + row.LastName,
			Age:    row.Age,
			About:  row.About,
			Gender: row.Gender,
		}
	}
	return users, nil
}

func parseSearchRequest(r *http.Request) (SearchRequest, error) {
	q := r.URL.Query()
	request := SearchRequest{
		Query:      q.Get("query"),
		OrderField: q.Get("order_field"),
	}
	var err error

	request.Limit, err = parseIntDefault(q.Get("limit"), 0)
	if err != nil {
		return request, fmt.Errorf("invalid limit: %s", q.Get("limit"))
	}
	if request.Limit < 0 {
		return request, fmt.Errorf("limit must be > 0")
	}

	request.Offset, err = parseIntDefault(q.Get("offset"), 0)
	if err != nil {
		return request, fmt.Errorf("invalid offset: %s", q.Get("offset"))
	}
	if request.Offset < 0 {
		return request, fmt.Errorf("offset must be > 0")
	}

	request.OrderBy, err = parseIntDefault(q.Get("order_by"), OrderByAsIs)
	if err != nil {
		return request, fmt.Errorf("invalid order: %s", q.Get("order_by"))
	}

	if request.OrderField == "" {
		request.OrderField = "Name"
	}
	return request, nil
}

func parseIntDefault(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	return strconv.Atoi(s)
}

func filterUsers(users []User, query string) []User {
	if query == "" {
		return users
	}
	q := strings.ToLower(query)
	result := make([]User, 0, len(users))

	for _, user := range users {
		if strings.Contains(strings.ToLower(user.Name), q) || strings.Contains(strings.ToLower(user.About), q) {
			result = append(result, user)
		}
	}
	return result
}

func sortUsers(users []User, orderField string, orderBy int) error {
	var less func(i, j int) bool

	switch strings.ToLower(orderField) {
	case "id":
		less = func(i, j int) bool { return users[i].ID < users[j].ID }
	case "age":
		less = func(i, j int) bool { return users[i].Age < users[j].Age }
	case "name":
		less = func(i, j int) bool { return users[i].Name < users[j].Name }
	default:
		return fmt.Errorf("%s", ErrorBadOrderField)
	}
	
	if orderBy != OrderByAsc && orderBy != OrderByDesc {
		return nil
	}

	sort.SliceStable(users, func(i, j int) bool {
		if orderBy == OrderByDesc {
			return less(j, i)
		}
		return less(i, j)
	})

	return nil
}

func paginateUser(users []User, offset, limit int) []User {
	if offset > len(users) {
		offset = len(users)
	}

	end := offset + limit
	if end > len(users) {
		end = len(users)
	}

	return users[offset:end]
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(SearchErrorResponse{Error: message})
}

func SearchServer(w http.ResponseWriter, r *http.Request) {
	request, err := parseSearchRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	users, err := loadUsers(datasetPath)
	if err != nil {
		http.Error(w, "SeachServer fatal error", http.StatusInternalServerError)
		return
	}

	users = filterUsers(users, request.Query)
	if err := sortUsers(users, request.OrderField, request.OrderBy); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	users = paginateUser(users, request.Offset, request.Limit)
	
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(users)
}
