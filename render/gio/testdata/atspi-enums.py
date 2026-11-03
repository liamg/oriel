import ctypes
a = ctypes.CDLL("libatspi.so.0")
g = ctypes.CDLL("libgobject-2.0.so.0")
class GEnumValue(ctypes.Structure):
    _fields_ = [("value", ctypes.c_int), ("name", ctypes.c_char_p), ("nick", ctypes.c_char_p)]
class GEnumClass(ctypes.Structure):
    _fields_ = [("g_type_class", ctypes.c_size_t), ("minimum", ctypes.c_int), ("maximum", ctypes.c_int), ("n_values", ctypes.c_uint), ("values", ctypes.POINTER(GEnumValue))]
g.g_type_class_ref.restype = ctypes.POINTER(GEnumClass)
for fn in ["atspi_role_get_type", "atspi_state_type_get_type", "atspi_coord_type_get_type", "atspi_component_layer_get_type", "atspi_text_granularity_get_type", "atspi_text_boundary_type_get_type"]:
    f = getattr(a, fn); f.restype = ctypes.c_size_t
    c = g.g_type_class_ref(f()).contents
    for i in range(c.n_values):
        v = c.values[i]
        print(v.name.decode(), v.value)
a.atspi_role_get_name.restype = ctypes.c_void_p
g2 = ctypes.CDLL("libglib-2.0.so.0")
for r in range(0, 130):
    p = a.atspi_role_get_name(r)
    if p:
        print("name", r, ctypes.cast(p, ctypes.c_char_p).value.decode())
        g2.g_free(ctypes.c_void_p(p))
