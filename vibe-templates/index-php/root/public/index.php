<?php
declare(strict_types=1);

$dbPath = getenv('DB_PATH') ?: dirname(__DIR__) . '/data/app.sqlite';
$dbDir = dirname($dbPath);
if (!is_dir($dbDir) && !mkdir($dbDir, 0700, true) && !is_dir($dbDir)) {
    http_response_code(500);
    throw new RuntimeException('Unable to create SQLite data directory');
}

$db = new PDO('sqlite:' . $dbPath, null, null, [
    PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
    PDO::ATTR_DEFAULT_FETCH_MODE => PDO::FETCH_ASSOC,
]);
$db->exec('PRAGMA journal_mode=WAL');
$db->exec('PRAGMA busy_timeout=5000');
$db->exec('CREATE TABLE IF NOT EXISTS notes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)');

$path = parse_url($_SERVER['REQUEST_URI'] ?? '/', PHP_URL_PATH) ?: '/';
if ($path === '/healthz') {
    header('Content-Type: application/json');
    echo json_encode(['ok' => true]) . "\n";
    exit;
}

if (($_SERVER['REQUEST_METHOD'] ?? 'GET') === 'POST') {
    $body = trim((string)($_POST['body'] ?? ''));
    if ($body !== '') {
        $stmt = $db->prepare('INSERT INTO notes (body) VALUES (:body)');
        $stmt->execute(['body' => $body]);
    }
    header('Location: /', true, 303);
    exit;
}

$notes = $db->query('SELECT id, body, created_at FROM notes ORDER BY id DESC LIMIT 20')->fetchAll();

function h(string $value): string
{
    return htmlspecialchars($value, ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8');
}
?><!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>index.php</title>
  <style>
    :root { color-scheme: light dark; font-family: ui-sans-serif, system-ui, sans-serif; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: Canvas; color: CanvasText; }
    main { width: min(720px, calc(100vw - 32px)); padding: 48px 0; }
    h1 { font-size: clamp(2rem, 7vw, 4.5rem); margin: 0 0 8px; letter-spacing: 0; }
    p { margin: 0 0 24px; color: color-mix(in srgb, CanvasText 72%, Canvas); }
    form { display: flex; gap: 8px; margin-bottom: 24px; }
    input { flex: 1; min-width: 0; padding: 12px 14px; border: 1px solid color-mix(in srgb, CanvasText 20%, Canvas); border-radius: 6px; font: inherit; }
    button { padding: 12px 16px; border: 0; border-radius: 6px; font: inherit; font-weight: 700; background: #1f7a4d; color: white; cursor: pointer; }
    ol { list-style: none; padding: 0; margin: 0; display: grid; gap: 8px; }
    li { border: 1px solid color-mix(in srgb, CanvasText 14%, Canvas); border-radius: 6px; padding: 12px 14px; }
    small { display: block; margin-top: 4px; color: color-mix(in srgb, CanvasText 58%, Canvas); }
  </style>
</head>
<body>
  <main>
    <h1>index.php</h1>
    <p>One PHP file, SQLite, one VM. Start small; split files only when the product earns it.</p>
    <form method="post">
      <input name="body" autocomplete="off" maxlength="240" placeholder="Ship a note" required>
      <button type="submit">Add</button>
    </form>
    <ol>
      <?php foreach ($notes as $note): ?>
        <li><?= h($note['body']) ?><small><?= h($note['created_at']) ?></small></li>
      <?php endforeach; ?>
    </ol>
  </main>
</body>
</html>
