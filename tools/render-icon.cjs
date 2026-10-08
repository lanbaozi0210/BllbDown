const fs = require('node:fs')
const path = require('node:path')
const { chromium } = require('playwright')

async function main() {
  const root = path.resolve(__dirname, '..')
  const assetDir = path.join(root, 'client', 'public')
  const svg = fs.readFileSync(path.join(assetDir, 'brand-mark.svg'), 'utf8')
  const browser = await chromium.launch({
    executablePath: process.env.CHROME_BINARY || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    headless: true,
  })
  const page = await browser.newPage({ viewport: { width: 128, height: 128 }, deviceScaleFactor: 1 })
  await page.setContent(`<body style="margin:0;background:transparent"><img id="icon" width="128" height="128" src="data:image/svg+xml;base64,${Buffer.from(svg).toString('base64')}"></body>`)
  const png = await page.locator('#icon').screenshot({ omitBackground: true })
  await browser.close()

  fs.writeFileSync(path.join(assetDir, 'favicon-32x32.png'), png)
  const header = Buffer.alloc(6)
  header.writeUInt16LE(1, 2)
  header.writeUInt16LE(1, 4)
  const entry = Buffer.alloc(16)
  entry[0] = 128
  entry[1] = 128
  entry.writeUInt16LE(1, 4)
  entry.writeUInt16LE(32, 6)
  entry.writeUInt32LE(png.length, 8)
  entry.writeUInt32LE(22, 12)
  fs.writeFileSync(path.join(assetDir, 'favicon.ico'), Buffer.concat([header, entry, png]))
}

main().catch(error => {
  process.stderr.write(`${error.stack || error}\n`)
  process.exitCode = 1
})
