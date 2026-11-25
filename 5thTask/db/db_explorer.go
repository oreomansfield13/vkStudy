package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type DBExplorer struct {
	db     *sql.DB
	tables map[string]*Table
}

type Table struct {
	Name    string
	Columns []*Column
	PK      string
}

type Column struct {
	Name      string
	DBType    string
	Nullable  bool
	IsPrimary bool
}

func (c *Column) Kind() string {
	dataType := strings.ToLower(c.DBType)
	if i := strings.IndexByte(dataType, '('); i >= 0 {
		dataType = dataType[:i]
	}
	dataType = strings.TrimSpace(dataType)
	switch dataType {
	case "int", "integer", "tinyint", "smallint", "mediumint", "bigint", "int2", "int4", "int8", "serial":
		return "int"
	case "float", "double", "real", "decimal", "numeric":
		return "float"
	default:
		return "string"
	}
}

func (explorer *DBExplorer) listTables() ([]string, error) {
	rows, err := explorer.db.Query("SHOW TABLES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func (explorer *DBExplorer) loadTable(name string) (*Table, error) {
	safe := strings.Replace(name, "`", "``", -1)
	rows, err := explorer.db.Query("SHOW FULL COLUMNS FROM `" + safe + "`")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	table := &Table{
		Name: name,
	}
	colNames, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		values := make([]interface{}, len(colNames))
		pointers := make([]interface{}, len(colNames))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}

		column := &Column{
			Name:      toString(values[0]),
			DBType:    toString(values[1]),
			Nullable:  toString(values[3]) == "YES",
			IsPrimary: toString(values[4]) == "PRI",
		}
		table.Columns = append(table.Columns, column)
		if column.IsPrimary {
			table.PK = column.Name
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return table, nil
}

func (explorer *DBExplorer) parsePath(path string) (table, id string, ok bool) {
	path = strings.Trim(path, "/")
	if path == "" {
		return "", "", true
	}
	parts := strings.Split(path, "/")
	if len(parts) == 1 {
		return parts[0], "", true
	}
	if len(parts) == 2 {
		return parts[0], parts[1], true
	}
	return "", "", false
}

func NewDBExplorer(db *sql.DB) (http.Handler, error) {
	explorer := &DBExplorer{
		db:     db,
		tables: make(map[string]*Table),
	}
	names, err := explorer.listTables()
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		table, err := explorer.loadTable(name)
		if err != nil {
			return nil, err
		}
		explorer.tables[name] = table
	}
	return explorer, nil
}

func (explorer *DBExplorer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	table, id, ok := explorer.parsePath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if table == "" {
		if r.Method == http.MethodGet {
			explorer.handleListTables(w, r)
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := explorer.tables[table]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown table")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if id == "" {
			explorer.handleListRecords(w, r, t)
		} else {
			explorer.handleGetRecord(w, r, t, id)
		}
	case http.MethodPut:
		if id == "" {
			explorer.handleCreateRecord(w, r, t)
		} else {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case http.MethodPost:
		if id == "" {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		} else {
			explorer.handleUpdateRecord(w, r, t, id)
		}
	case http.MethodDelete:
		if id == "" {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		} else {
			explorer.handleDeleteRecord(w, r, t, id)
		}
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (explorer *DBExplorer) handleListTables(w http.ResponseWriter, r *http.Request) {
	names := make([]string, 0, len(explorer.tables))
	for name := range explorer.tables {
		names = append(names, name)
	}
	sort.Strings(names)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"tables": names,
		},
	})
}

func (explorer *DBExplorer) handleListRecords(w http.ResponseWriter, r *http.Request, t *Table) {
	limit := 5
	offset := 0
	q := r.URL.Query()
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v >= 0 {
		limit = v
	}
	if v, err := strconv.Atoi(q.Get("offset")); err == nil && v >= 0 {
		offset = v
	}
	safeName := quoteIdent(t.Name)
	rows, err := explorer.db.Query(
		"SELECT * FROM "+safeName+" LIMIT ? OFFSET ?",
		limit, offset,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	records := make([]map[string]interface{}, 0)
	for rows.Next() {
		rec, err := scanRow(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"records": records,
		},
	})
}

func (explorer *DBExplorer) handleGetRecord(w http.ResponseWriter, r *http.Request, t *Table, id string) {
	if t.PK == "" {
		writeError(w, http.StatusInternalServerError, "table has no primary key")
		return
	}
	sqlText := "SELECT * FROM " + quoteIdent(t.Name) + " WHERE " + quoteIdent(t.PK) + " = ? LIMIT 1"
	rows, err := explorer.db.Query(sqlText, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeError(w, http.StatusNotFound, "record not found")
		return
	}
	record, err := scanRow(rows)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"record": record,
		},
	})
}

func (explorer *DBExplorer) handleCreateRecord(w http.ResponseWriter, r *http.Request, t *Table) {
	if t.PK == "" {
		writeError(w, http.StatusInternalServerError, "table has no primary key")
		return
	}
	body, err := decodeBody(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var (
		columns   []string
		arguments []interface{}
	)
	for _, column := range t.Columns {
		if column.IsPrimary {
			continue
		}
		raw, present := body[column.Name]
		if !present {
			var v interface{}
			if column.Nullable {
				v = nil
			} else {
				v = zeroValue(column)
			}
			columns = append(columns, quoteIdent(column.Name))
			arguments = append(arguments, v)
			continue
		}
		v, ok := convertValue(column, raw)
		if !ok {
			writeError(w, http.StatusBadRequest, "field "+column.Name+" have invalid type")
			return
		}
		columns = append(columns, quoteIdent(column.Name))
		arguments = append(arguments, v)
	}

	placeholders := make([]string, len(columns))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	query := "INSERT INTO " + quoteIdent(t.Name) + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	res, err := explorer.db.Exec(query, arguments...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	lastID, err := res.LastInsertId()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			t.PK: lastID,
		},
	})
}

func (explorer *DBExplorer) handleUpdateRecord(w http.ResponseWriter, r *http.Request, t *Table, id string) {
	if t.PK == "" {
		writeError(w, http.StatusInternalServerError, "table has no primary key")
		return
	}
	body, err := decodeBody(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var (
		sets      []string
		arguments []interface{}
	)
	for _, column := range t.Columns {
		raw, present := body[column.Name]
		if !present {
			continue
		}
		if column.IsPrimary {
			writeError(w, http.StatusBadRequest, "field "+column.Name+" have invalid type")
			return
		}
		v, ok := convertValue(column, raw)
		if !ok {
			writeError(w, http.StatusBadRequest, "field "+column.Name+" have invalid type")
			return
		}
		sets = append(sets, quoteIdent(column.Name)+" = ?")
		arguments = append(arguments, v)
	}

	if len(sets) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": map[string]interface{}{
				"updated": 0,
			},
		})
		return
	}
	query := "UPDATE " + quoteIdent(t.Name) + " SET " + strings.Join(sets, ", ") + " WHERE " + quoteIdent(t.PK) + " = ?"
	arguments = append(arguments, id)
	res, err := explorer.db.Exec(query, arguments...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	affected, err := res.RowsAffected()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"updated": affected,
		},
	})
}

func (explorer *DBExplorer) handleDeleteRecord(w http.ResponseWriter, r *http.Request, t *Table, id string) {
	if t.PK == "" {
		writeError(w, http.StatusInternalServerError, "table has no primary key")
		return
	}
	query := "DELETE FROM " + quoteIdent(t.Name) + " WHERE " + quoteIdent(t.PK) + " = ?"
	res, err := explorer.db.Exec(query, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	affected, err := res.RowsAffected()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"deleted": affected,
		},
	})
}

func toString(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload != nil {
		_ = json.NewEncoder(w).Encode(payload)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{"error": msg})
}

func scanRow(rows *sql.Rows) (map[string]interface{}, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	values := make([]interface{}, len(columns))
	pointers := make([]interface{}, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	if err := rows.Scan(pointers...); err != nil {
		return nil, err
	}
	out := make(map[string]interface{}, len(columns))
	for i, name := range columns {
		out[name] = normalizeDBValue(values[i])
	}
	return out, nil
}

func normalizeDBValue(v interface{}) interface{} {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(x)
	case int64, float64, bool, string:
		return x
	case time.Time:
		return x.Format(time.RFC3339)
	default:
		return x
	}
}

func decodeBody(r *http.Request) (map[string]interface{}, error) {
	if r.Body == nil {
		return map[string]interface{}{}, nil
	}
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	var raw map[string]interface{}
	if err := decoder.Decode(&raw); err != nil {
		if err == io.EOF {
			return map[string]interface{}{}, nil
		}
		return nil, err
	}
	if raw == nil {
		raw = map[string]interface{}{}
	}
	return raw, nil
}

func convertValue(column *Column, raw interface{}) (interface{}, bool) {
	if raw == nil {
		if column.Nullable {
			return nil, true
		}
		return nil, false
	}
	switch column.Kind() {
	case "int":
		n, isNum := raw.(json.Number)
		if !isNum {
			return nil, false
		}
		i, err := n.Int64()
		if err != nil {
			return nil, false
		}
		return i, true
	case "float":
		n, isNum := raw.(json.Number)
		if !isNum {
			return nil, false
		}
		i, err := n.Float64()
		if err != nil {
			return nil, false
		}
		return i, true
	default:
		s, isStr := raw.(string)
		if !isStr {
			return nil, false
		}
		return s, true
	}
}

func quoteIdent(name string) string {
	return "`" + strings.Replace(name, "`", "``", -1) + "`"
}

func zeroValue(column *Column) interface{} {
	switch column.Kind() {
	case "int":
		return int64(0)
	case "float":
		return float64(0)
	default:
		return ""
	}
}
