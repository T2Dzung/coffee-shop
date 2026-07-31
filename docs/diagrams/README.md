# Diagram workflow

The following Excalidraw files are the current editable sources of truth for the
platform diagrams:

| Diagram | Editable source | Published SVG |
| --- | --- | --- |
| DEV architecture | [`dev-architecture-v2.excalidraw`](dev-architecture-v2.excalidraw) | [`../img/dev-architecture.svg`](../img/dev-architecture.svg) |
| PROD architecture | [`prod-architecture-v2.excalidraw`](prod-architecture-v2.excalidraw) | [`../img/prod-architecture.svg`](../img/prod-architecture.svg) |
| Delivery flow | [`delivery-flow-v2.excalidraw`](delivery-flow-v2.excalidraw) | [`../img/delivery-flow.svg`](../img/delivery-flow.svg) |

The `-v2` suffix identifies the current visual design. Published SVG names remain
stable so README and documentation links do not change when an editable source is
redesigned.

To change a platform diagram:

1. open the mapped `*-v2.excalidraw` source in Excalidraw;
2. keep the established palette, grouped boundaries and right-angle flow style where
   practical;
3. keep labels concise and put detailed explanation in
   [`../architecture.md`](../architecture.md);
4. export the drawing as SVG using the published filename in the table;
5. replace the matching file under [`../img/`](../img/);
6. open the SVG at normal README width and verify that labels, arrows and boundaries are
   readable;
7. commit the Excalidraw source and exported SVG together.

Do not edit only the SVG: the next export would discard that change. There is currently
no repository-owned renderer, so the public documentation does not claim that diagram
generation is automatic.

`coffeeshop-flow.excalidraw`, `coffeeshop-flow-hashicorp.excalidraw` and
`clean_ddd.excalidraw` are retained upstream application/reference drawings. They are
not editable sources for the three platform diagrams above.
