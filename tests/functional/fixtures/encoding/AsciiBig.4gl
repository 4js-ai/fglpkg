PUBLIC TYPE tOrderLine RECORD
   lineId   INTEGER,
   partName VARCHAR(60),
   quantity INTEGER,
   unitCost DECIMAL(10,2)
END RECORD

PUBLIC FUNCTION summarize(lines DYNAMIC ARRAY OF tOrderLine) RETURNS (INTEGER, STRING)
   VAR total DECIMAL(12,2) = 0
   VAR idx INTEGER = 0
   VAR label STRING
   FOR idx = 1 TO lines.getLength()
      LET total = total + (lines[idx].quantity * lines[idx].unitCost)
   END FOR
   LET label = SFMT("%1 lines, total %2", lines.getLength(), total)
   RETURN lines.getLength(), label
END FUNCTION
