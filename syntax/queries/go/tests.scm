; Tests for testrun's Go adapter. Ours, not nvim-treesitter's.
; func TestXxx(t *testing.T)
((function_declaration
  name: (identifier) @test.name
  parameters: (parameter_list
    .
    (parameter_declaration
      type: (pointer_type
        (qualified_type
          package: (package_identifier) @_pkg
          name: (type_identifier) @_type)))
    .)) @test.func
  (#match? @test.name "^Test([^\\p{Ll}]|$)")
  (#eq? @_pkg "testing")
  (#eq? @_type "T"))

; t.Run("name", …)
((call_expression
  function: (selector_expression
    field: (field_identifier) @_run)
  arguments: (argument_list
    .
    [(interpreted_string_literal) (raw_string_literal)] @test.name)) @test.run
  (#eq? @_run "Run"))

; t.Run(tc.name, …) and t.Run(name, …) in a loop over a table
((call_expression
  function: (selector_expression
    field: (field_identifier) @_run)
  arguments: (argument_list
    .
    (selector_expression
      operand: (identifier) @run.var
      field: (field_identifier) @run.field))) @run.table
  (#eq? @_run "Run"))

((call_expression
  function: (selector_expression
    field: (field_identifier) @_run)
  arguments: (argument_list
    .
    (identifier) @run.var)) @run.table
  (#eq? @_run "Run"))

(for_statement
  (range_clause
    left: (expression_list
      .
      (identifier) @loop.key
      (identifier)? @loop.value .)
    right: (_) @loop.source)) @loop

; tests := []struct{…}{…}, var tests = …
(short_var_declaration
  left: (expression_list . (identifier) @table.name .)
  right: (expression_list . (composite_literal) @table.literal .))

(var_spec
  name: (identifier) @table.name
  value: (expression_list . (composite_literal) @table.literal .))

; a table's rows, and the fields of its row type in order
(composite_literal
  body: (literal_value
    (literal_element
      (literal_value) @row)))

(composite_literal
  body: (literal_value
    (keyed_element
      .
      (literal_element
        [(interpreted_string_literal) (raw_string_literal)] @row.key)
      (literal_element
        (literal_value) @row))))

(literal_value
  (keyed_element
    .
    (literal_element
      (identifier) @field.key)
    (literal_element) @field.value))

(literal_value
  (literal_element) @row.element)

(field_declaration
  name: (field_identifier) @struct.field) @struct.decl
