package squirrel

import (
	"database/sql/driver"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

type expr struct {
	sql  string
	args []interface{}
}

// Expr builds value expressions for InsertBuilder and UpdateBuilder.
//
// Ex:
//     .Values(Expr("FROM_UNIXTIME(?)", t))
func Expr(sql string, args ...interface{}) expr {
	return expr{sql: sql, args: args}
}

func (e expr) ToSQL() (sql string, args []interface{}, err error) {
	return e.sql, e.args, nil
}

type exprs []expr

func (es exprs) AppendToSQL(w io.Writer, sep string, args []interface{}) ([]interface{}, error) {
	for i, e := range es {
		if i > 0 {
			_, err := io.WriteString(w, sep)
			if err != nil {
				return nil, err
			}
		}
		_, err := io.WriteString(w, e.sql)
		if err != nil {
			return nil, err
		}
		args = append(args, e.args...)
	}
	return args, nil
}

// aliasExpr helps to alias part of SQL query generated with underlying "expr"
type aliasExpr struct {
	expr  Sqlizer
	alias string
}

// Alias allows to define alias for column in SelectBuilder. Useful when column is
// defined as complex expression like IF or CASE
// Ex:
//		.Column(Alias(caseStmt, "case_column"))
func Alias(expr Sqlizer, alias string) aliasExpr {
	return aliasExpr{expr, alias}
}

func (e aliasExpr) ToSQL() (sql string, args []interface{}, err error) {
	sql, args, err = e.expr.ToSQL()
	if err == nil {
		sql = fmt.Sprintf("(%s) AS %s", sql, e.alias)
	}
	return
}

// GenerateOrderPredicateIndex provides a slice of keys useful for ordering predicates.
func GenerateOrderPredicateIndex(predicates map[string]interface{}) []string {
	keys := make([]string, len(predicates))
	counter := 0
	for key := range predicates {
		keys[counter] = key
		counter++
	}
	sort.Strings(keys)
	return keys
}

// Eq is syntactic sugar for use with Where/Having/Set methods.
// Ex:
//     .Where(Eq{"id": 1})
type Eq map[string]interface{}

func (eq Eq) toSQL(useNotOpr bool) (sql string, args []interface{}, err error) {
	var (
		exprs    []string
		equalOpr = "="
		inOpr    = "IN"
		nullOpr  = "IS"
	)

	if useNotOpr {
		equalOpr = "<>"
		inOpr = "NOT IN"
		nullOpr = "IS NOT"
	}

	predicateIndex := GenerateOrderPredicateIndex(eq)

	for _, key := range predicateIndex {
		val := eq[key]

		var expr string

		switch v := val.(type) {
		case driver.Valuer:
			if val, err = v.Value(); err != nil {
				return
			}
		}

		if val == nil {
			expr = fmt.Sprintf("%s %s NULL", key, nullOpr)
		} else {
			valVal := reflect.ValueOf(val)
			if valVal.Kind() == reflect.Array || valVal.Kind() == reflect.Slice {
				if valVal.Len() == 0 {
					expr = fmt.Sprintf("%s %s (NULL)", key, inOpr)
					if args == nil {
						args = []interface{}{}
					}
				} else {
					for i := 0; i < valVal.Len(); i++ {
						args = append(args, valVal.Index(i).Interface())
					}
					expr = fmt.Sprintf("%s %s (%s)", key, inOpr, Placeholders(valVal.Len()))
				}
			} else {
				expr = fmt.Sprintf("%s %s ?", key, equalOpr)
				args = append(args, val)
			}
		}
		exprs = append(exprs, expr)
	}
	sql = strings.Join(exprs, " AND ")
	return
}

func (eq Eq) ToSQL() (sql string, args []interface{}, err error) {
	return eq.toSQL(false)
}

// NotEq is syntactic sugar for use with Where/Having/Set methods.
// Ex:
//     .Where(NotEq{"id": 1}) == "id <> 1"
type NotEq Eq

func (neq NotEq) ToSQL() (sql string, args []interface{}, err error) {
	return Eq(neq).toSQL(true)
}

// Lt is syntactic sugar for use with Where/Having/Set methods.
// Ex:
//     .Where(Lt{"id": 1})
type Lt map[string]interface{}

func (lt Lt) toSQL(opposite, orEq bool) (sql string, args []interface{}, err error) {
	var (
		exprs []string
		opr   = "<"
	)

	if opposite {
		opr = ">"
	}

	if orEq {
		opr = fmt.Sprintf("%s%s", opr, "=")
	}

	predicateIndex := GenerateOrderPredicateIndex(lt)

	for _, key := range predicateIndex {
		val := lt[key]

		switch v := val.(type) {
		case driver.Valuer:
			if val, err = v.Value(); err != nil {
				return
			}
		}

		if val == nil {
			err = fmt.Errorf("cannot use null with less than or greater than operators")
			return
		}

		valVal := reflect.ValueOf(val)
		if valVal.Kind() == reflect.Array || valVal.Kind() == reflect.Slice {
			err = fmt.Errorf("cannot use array or slice with less than or greater than operators")
			return
		}

		expr := fmt.Sprintf("%s %s ?", key, opr)
		args = append(args, val)
		exprs = append(exprs, expr)
	}
	sql = strings.Join(exprs, " AND ")
	return
}

func (lt Lt) ToSQL() (sql string, args []interface{}, err error) {
	return lt.toSQL(false, false)
}

// LtOrEq is syntactic sugar for use with Where/Having/Set methods.
// Ex:
//     .Where(LtOrEq{"id": 1}) == "id <= 1"
type LtOrEq Lt

func (ltOrEq LtOrEq) ToSQL() (sql string, args []interface{}, err error) {
	return Lt(ltOrEq).toSQL(false, true)
}

// Gt is syntactic sugar for use with Where/Having/Set methods.
// Ex:
//     .Where(Gt{"id": 1}) == "id > 1"
type Gt Lt

func (gt Gt) ToSQL() (sql string, args []interface{}, err error) {
	return Lt(gt).toSQL(true, false)
}

// GtOrEq is syntactic sugar for use with Where/Having/Set methods.
// Ex:
//     .Where(GtOrEq{"id": 1}) == "id >= 1"
type GtOrEq Lt

func (gtOrEq GtOrEq) ToSQL() (sql string, args []interface{}, err error) {
	return Lt(gtOrEq).toSQL(true, true)
}

type conj []Sqlizer

func (c conj) join(sep string) (sql string, args []interface{}, err error) {
	var sqlParts []string
	for _, sqlizer := range c {
		partSQL, partArgs, err := sqlizer.ToSQL()
		if err != nil {
			return "", nil, err
		}
		if partSQL != "" {
			sqlParts = append(sqlParts, partSQL)
			args = append(args, partArgs...)
		}
	}
	if len(sqlParts) > 0 {
		sql = fmt.Sprintf("(%s)", strings.Join(sqlParts, sep))
	}
	return
}

type And conj

func (a And) ToSQL() (string, []interface{}, error) {
	return conj(a).join(" AND ")
}

type Or conj

func (o Or) ToSQL() (string, []interface{}, error) {
	return conj(o).join(" OR ")
}
