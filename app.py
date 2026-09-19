import hashlib
import time
from flask import Flask, jsonify, request

app = Flask(__name__)

@app.route("/", methods=["GET"])
def index():
    return "Hello, world - app de prueba del autoscaler\n", 200

@app.route("/health", methods=["GET"])
def health():
    return jsonify({"status": "ok"}), 200

@app.route("/work", methods=["GET"])
def work():
    raw_iterations = request.args.get("iterations", default="100000")
    try:
        iterations = int(raw_iterations)
    except ValueError:
        return "parametro 'iterations' invalido\n", 400

    start_time = time.time()
    digest = b"si3016-autoscaler"
    for _ in range(iterations):
        digest = hashlib.sha256(digest).digest()
    elapsed_seconds = time.time() - start_time

    return jsonify({
        "iterations": iterations,
        "elapsed_seconds": elapsed_seconds
    }), 200

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=8080, threaded=True)