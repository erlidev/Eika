# Rounding rule

- Each line (`quantity * unit_price`) is rounded to whole cents, half to even,
  so 2.345 becomes 2.34 and 2.355 becomes 2.36.
- A customer's net is the sum of its rounded lines.
- VAT is 19% of the customer's net, rounded half to even once, on the net and
  never per line.
- A customer's total is net plus VAT. The grand total is the sum of the
  customers' totals.
