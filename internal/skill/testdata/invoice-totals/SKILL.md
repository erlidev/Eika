---
name: invoice-totals
description: Total invoice lines per customer with the company's VAT and rounding rules. Use when the user gives invoice lines as CSV, or asks for invoice totals, VAT or a customer statement.
license: MIT
compatibility: Needs python3 on the computer; no network.
metadata:
  version: "1.0"
---

# Invoice totals

1. Put the CSV (columns `customer,item,quantity,unit_price`) on the computer.
2. Run `python3 /skills/invoice-totals/scripts/totals.py FILE.csv`.
3. Report its output unchanged. Do not recompute it: the rounding rule is in
   `references/rounding.md` and the script applies it.
