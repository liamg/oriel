; Tests for testrun's pytest adapter. Ours, not nvim-treesitter's.
((function_definition
  name: (identifier) @test.name) @test.func
  (#match? @test.name "^test"))

(function_definition) @def

((function_definition
  name: (identifier) @_init) @init
  (#eq? @_init "__init__"))

(class_definition
  name: (identifier) @suite.name) @suite

; @pytest.mark.parametrize("a,b", [...], ids=[...]): the decorator, its names and values, and the function
(decorated_definition
  (decorator
    (call
      function: (attribute
        attribute: (identifier) @_parametrize)
      arguments: (argument_list
        .
        (_) @param.names
        .
        [(list) (tuple)] @param.values))) @param
  definition: (function_definition) @param.target
  (#eq? @_parametrize "parametrize"))

(call
  function: (attribute
    attribute: (identifier) @_parametrize)
  arguments: (argument_list
    .
    (_)
    .
    [(list (_) @param.value) (tuple (_) @param.value)])
  (#eq? @_parametrize "parametrize"))

(keyword_argument
  name: (identifier) @_ids
  value: (list (_)* @param.id) @param.ids
  (#eq? @_ids "ids"))
