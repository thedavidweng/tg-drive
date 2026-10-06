import { useEffect, useState } from "react"

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useI18n } from "@/i18n"
import { OfficeView, type OfficeRendererProps } from "@/preview/office"
import type { PreviewProvider, PreviewViewProps } from "@/preview/types"

// A DOM table past these sizes stalls the webview; the rest stays a Download away.
const MAX_ROWS = 1000
const MAX_COLUMNS = 100

interface Sheet {
  name: string
  rows: string[][]
  rowCount: number
  columnCount: number
}

/** "A", "B", …, "Z", "AA": the spreadsheet column label for a 1-based index. */
function columnLabel(n: number): string {
  let label = ""
  for (; n > 0; n = Math.floor((n - 1) / 26)) label = String.fromCharCode(65 + ((n - 1) % 26)) + label
  return label
}

async function readSheets(data: ArrayBuffer): Promise<Sheet[]> {
  const { default: ExcelJS } = await import("exceljs")
  const workbook = new ExcelJS.Workbook()
  await workbook.xlsx.load(data)
  return workbook.worksheets
    .filter((ws) => ws.state !== "veryHidden")
    .map((ws) => {
      const rowCount = ws.rowCount
      const columnCount = ws.columnCount
      const rows: string[][] = []
      for (let r = 1; r <= Math.min(rowCount, MAX_ROWS); r++) {
        const row = ws.getRow(r)
        const cells: string[] = []
        for (let c = 1; c <= Math.min(columnCount, MAX_COLUMNS); c++) {
          let text = ""
          try {
            text = row.getCell(c).text
          } catch {
            // A value ExcelJS cannot format (a broken formula result) shows as an empty cell.
          }
          cells.push(text)
        }
        rows.push(cells)
      }
      return { name: ws.name, rows, rowCount, columnCount }
    })
}

function XlsxRenderer({ data, onError }: OfficeRendererProps) {
  const { t } = useI18n()
  const [sheets, setSheets] = useState<Sheet[] | null>(null)

  useEffect(() => {
    let live = true
    readSheets(data).then(
      (s) => {
        if (!live) return
        if (s.length === 0) onError()
        else setSheets(s)
      },
      () => live && onError(),
    )
    return () => {
      live = false
    }
  }, [data, onError])

  if (!sheets) return <p className="py-16 text-center text-muted-foreground">{t("preview.loading")}</p>
  return (
    <Tabs defaultValue="0" className="h-full gap-0">
      <TabsList aria-label={t("preview.xlsx.sheets")} className="m-2 h-7 max-w-[calc(100%-1rem)] justify-start overflow-x-auto">
        {sheets.map((s, i) => (
          <TabsTrigger key={i} value={String(i)} className="h-6 flex-none px-3 text-[12.5px] font-normal">
            {s.name}
          </TabsTrigger>
        ))}
      </TabsList>
      {sheets.map((s, i) => (
        <TabsContent key={i} value={String(i)} className="min-h-0 overflow-auto px-2 pb-2">
          <SheetTable sheet={s} />
        </TabsContent>
      ))}
    </Tabs>
  )
}

function SheetTable({ sheet }: { sheet: Sheet }) {
  const { t } = useI18n()
  const shownColumns = Math.min(sheet.columnCount, MAX_COLUMNS)
  const clipped = sheet.rowCount > MAX_ROWS || sheet.columnCount > MAX_COLUMNS
  return (
    <>
      {clipped && (
        <p role="status" className="mb-2 text-[12px] text-muted-foreground">
          {t("preview.xlsx.clipped", {
            rows: Math.min(sheet.rowCount, MAX_ROWS),
            columns: shownColumns,
          })}
        </p>
      )}
      <table aria-label={sheet.name} className="border-collapse text-[12px] tabular-nums">
        <thead>
          <tr>
            <th className="sticky top-0 border border-line bg-card px-2 py-1" />
            {Array.from({ length: shownColumns }, (_, c) => (
              <th key={c} scope="col" className="sticky top-0 border border-line bg-card px-2 py-1 font-medium text-muted-foreground">
                {columnLabel(c + 1)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {sheet.rows.map((cells, r) => (
            <tr key={r}>
              <th scope="row" className="border border-line bg-card px-2 py-1 text-right font-medium text-muted-foreground">
                {r + 1}
              </th>
              {cells.map((text, c) => (
                <td key={c} className="max-w-[320px] truncate border border-line px-2 py-1 whitespace-pre" title={text || undefined}>
                  {text}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </>
  )
}

function XlsxPreview(props: PreviewViewProps) {
  return <OfficeView {...props} Renderer={XlsxRenderer} />
}

export const xlsxPreview: PreviewProvider = {
  id: "xlsx",
  mimeTypes: ["application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"],
  extensions: ["xlsx"],
  View: XlsxPreview,
}
