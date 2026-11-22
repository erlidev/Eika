#!/usr/bin/env python3
"""Total invoice lines per customer: 19% VAT, half-even rounding to cents."""
import csv
import sys
from decimal import ROUND_HALF_EVEN, Decimal, InvalidOperation

CENT = Decimal("0.01")


def cents(x):
    return x.quantize(CENT, rounding=ROUND_HALF_EVEN)


def main(path):
    nets = {}
    with open(path, newline="") as f:
        for n, row in enumerate(csv.DictReader(f), start=2):
            try:
                line = cents(Decimal(row["quantity"]) * Decimal(row["unit_price"]))
                customer = row["customer"].strip()
            except (KeyError, AttributeError, InvalidOperation):
                sys.exit(f"line {n}: bad row")
            if not customer:
                sys.exit(f"line {n}: bad row")
            nets[customer] = nets.get(customer, Decimal(0)) + line
    grand = Decimal(0)
    for customer in sorted(nets):
        net = nets[customer]
        vat = cents(net * Decimal("0.19"))
        grand += net + vat
        print(f"{customer}: net {net}, vat {vat}, total {net + vat}")
    print(f"GRAND TOTAL {grand}")


if len(sys.argv) != 2:
    sys.exit("usage: totals.py FILE.csv")
try:
    main(sys.argv[1])
except OSError as e:
    sys.exit(f"cannot read file: {e}")
