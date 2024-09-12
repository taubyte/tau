# mock auth node
import json
import sys
import random
import logging
from flask import Flask, request, jsonify
from flask_cors import CORS
import uuid

app = Flask(__name__)
CORS(app)


def verify_headers():
    headers = request.headers
    if request.method == 'OPTIONS':
        return True
    if "Authorization" not in headers:
        return False
    if headers["Authorization"] != "github " + '123456':
        return False
    return True


@app.before_request
def before_request():
    try:
        if not verify_headers():
            return jsonify({"error": "invalid headers"}), 401
        else:
            return None
    except:
        return None

global data

data = {
    "test_user": {
        "info": {
            "company": "",
            "email": "",
            "login": "test_user",
            "name": ""
        },
        "repositories": {},
        "projects": [],
        "devices": {},
        "domains": {},
    }
}


def random_id(numerical=False):
    if numerical:
        return "{}".format(random.randint(100000000, 999999999))
    else:
        return uuid.uuid4().hex[:12]


@app.route("/me", methods=["GET", "OPTIONS"])
def me():
    if request.method == "OPTIONS":
        return "", 200
    ret = {
        "user": data["test_user"]["info"]
    }
    return jsonify(ret)

# in auth we'll need to list a user's

@app.route("/domain/<fqdn>/for/<project_id>", methods=["POST", "OPTIONS"])
def register_domain(fqdn, project_id):
    if request.method == "OPTIONS":
        return "", 200
    if request.method == "POST":
        domains = data["test_user"]["domains"]
        if project_id not in domains:
            domains[project_id] = {}
        token = uuid.uuid4().hex[:12]
        domains[project_id][fqdn] = token

        return jsonify({
            "code": 200,
            "token": token,
            "entry": project_id[0:8]+"."+fqdn,
            "type": "txt"
        })


@app.route("/repository/<provider>/<repo_id>", methods=["GET", "PUT", "DELETE", "OPTIONS"])
def register_repository(provider, repo_id):
    if request.method == "OPTIONS":
        return "", 200
    if request.method == "PUT":
        data["test_user"]["repositories"][repo_id] = {
            "provider": provider,
            "id": repo_id
        }
        return jsonify({
            "key": f"repository/{provider}/{repo_id}",
            "code": 200,
        })
    if request.method == "DELETE":
        if repo_id in data["test_user"]["repositories"]:
            del data["test_user"]["repositories"][repo_id]
            return jsonify({
                "code": 200,
            })
        else:
            return jsonify({"error": "repository not found"}), 404
    
    if request.method == "GET":
        if repo_id in data["test_user"]["repositories"]:
            return jsonify(data["test_user"]["repositories"][repo_id])
        else:
            return jsonify({"error": "repository not found"}), 404


@app.route("/projects", methods=["GET", "OPTIONS"])
def projects():
    if request.method == "OPTIONS":
        return "", 200
    projects = data["test_user"]["projects"]
    ret = {
        "projects": [
            {
                "id": project['id'],
                "name": project['name']
            }
            for project in projects
        ]
    }
    return jsonify(ret)


@app.route("/projects/<project_id>", methods=["GET", "OPTIONS"])
def project(project_id):
    if request.method == "OPTIONS":
        return "", 200
    projects = data["test_user"]["projects"]
    for project in projects:
        if project['id'] == project_id:
            ret = {
                "project": project
            }
            return jsonify(ret)
    return "", 404


@app.route("/project/new/<project_name>", methods=["POST", "OPTIONS"])
def projects_new(project_name):
    if request.method == "OPTIONS":
        return "", 200
    config_repo = request.json['config']

    if config_repo['id'] not in data["test_user"]["repositories"]:
        return jsonify({"error": "config repository not found"}), 404
    code_repo = request.json['code']
    if code_repo['id'] not in data["test_user"]["repositories"]:
        return jsonify({"error": "code repository not found"}), 404
    new_project = {
        "id": random_id(),
        "name": project_name,
        "Repositories": {
            "code": {
                "fullname": f"test_user/tb_code_{project_name}",
                "id": random_id(True),
                "name": f"tb_code_{project_name}",
                "url": f"https://api.github.com/repos/test_user/tb_code_{project_name}"
            },
            "configuration": {
                "fullname": f"test_user/tb_{project_name}",
                "id": random_id(True),
                "name": f"tb_{project_name}",
                "url": f"https://api.github.com/repos/test_user/tb_{project_name}"
            },
            "provider": "github"
        }
    }
    data["test_user"]['projects'].append(new_project)
    ret = {
        "project": new_project
    }

    return jsonify(ret)


@ app.route("/projects/<project_id>/libraries/new/<library_name>", methods=["POST", "OPTIONS"])
def projects_libraries_new(project_id, library_name):
    if request.method == "OPTIONS":
        return "", 200
    repo = request.json['repository']
    if repo['id'] not in data["test_user"]["repositories"]:
        return jsonify({"error": "repository not found"}), 404
    new_library = {
        "library": {
            "id": random_id(),
            "name": library_name,
            "repository": repo
        }
    }
    for project in data["test_user"]['projects']:
        if project['id'] == project_id:
            data["test_user"]['libraries'][new_library['library']
                                          ['id']] = new_library['library']
            return jsonify(new_library)



@ app.route("/projects/<project_id>/websites/new/<website_name>", methods=["POST", "OPTIONS"])
def projects_websites_new(project_id, website_name):
    if request.method == "OPTIONS":
        return "", 200
    repo = request.json['repository']
    if repo['id'] not in data["test_user"]["repositories"]:
        return jsonify({"error": "repository not found"}), 404
    new_website = {
        "website": {
            "id": random_id(),
            "name": website_name,
            "repository": repo
        }
    }
    for project in data["test_user"]['projects']:
        if project['id'] == project_id:
            data["test_user"]['websites'][new_website['website']
                                          ['id']] = new_website['website']
            return jsonify(new_website)


@app.route("/project/<project_id>/devices", methods=["GET", "OPTIONS"])
def project_devices(project_id):
    if request.method == "OPTIONS":
        return "", 200
    try:
        devices = data["test_user"]["devices"][project_id]
        ret_devices = []
        for device in devices:
            ret_devices.append(device['id'])
        return jsonify({"devices": ret_devices})
    except KeyError:
        return jsonify({"devices": []})


@app.route("/project/<project_id>/devices/new", methods=["POST", "OPTIONS"])
def project_devices_new(project_id):
    if request.method == "OPTIONS":
        return "", 200
    new_device = {
        "description": request.json['description'],
        "enabled": True,
        "id": random_id(),
        "name": request.json['name'],
        "publicKey": "CAES" + random_id(),
        "privateKey": "CAES" + random_id() + random_id(),
        "tags": request.json['tags'],
        "type": request.json['type'],
        "env": request.json['env']
    }
    try:
        data["test_user"]["devices"][project_id].append(new_device)
    except KeyError:
        data["test_user"]["devices"][project_id] = [new_device]
    return jsonify({
        "id": new_device['id'],
        "privateKey": new_device['privateKey'],
        "publicKey": new_device['publicKey']
    })


@app.route("/project/<project_id>/device/<device_id>", methods=["GET", "OPTIONS"])
def project_devices_id(project_id, device_id):
    if request.method == "OPTIONS":
        return "", 200
    try:
        devices = data["test_user"]["devices"][project_id]
        for device in devices:
            if device['id'] == device_id:
                device_copy = device.copy()
                device_copy.pop('privateKey')
                return jsonify(device_copy)
        raise KeyError
    except KeyError:
        return jsonify({
            "code": 400,
            "error": "datastore: key not found"
        })


@app.route("/project/<project_id>/device/<device_id>", methods=["PUT", "OPTIONS"])
def update_device(project_id, device_id):
    if request.method == "OPTIONS":
        return "", 200
    try:
        devices = data["test_user"]["devices"][project_id]
        for device in devices:
            if device['id'] == device_id:
                device['name'] = request.json['name']
                device['description'] = request.json['description']
                device['tags'] = request.json['tags']
                device['type'] = request.json['type']
                device['env'] = request.json['env']
                return jsonify({"id": device['id']})
        raise KeyError
    except KeyError as e:
        return jsonify({
            "code": 400,
            "error": "datastore: key not found"
        })


@app.route("/project/<project_id>/devices/disable", methods=["PUT", "OPTIONS"])
def project_devices_disable(project_id):
    if request.method == "OPTIONS":
        return "", 200
    ids = request.json['ids']
    try:
        changed = []
        devices = data["test_user"]["devices"][project_id]
        for idx, device in enumerate(devices):
            if device['id'] in ids:
                changed.append(device['id'])
                data["test_user"]["devices"][project_id][idx]['enabled'] = False
        return jsonify({
            "changed": changed,
            "errors": {}
        })
    except KeyError:
        return jsonify({
            "code": 400,
            "error": "datastore: key not found"
        })


@app.route("/project/<project_id>/devices/enable", methods=["PUT", "OPTIONS"])
def project_devices_enable(project_id):
    if request.method == "OPTIONS":
        return "", 200
    ids = request.json['ids']
    try:
        changed = []
        devices = data["test_user"]["devices"][project_id]
        for idx, device in enumerate(devices):
            if device['id'] in ids:
                changed.append(device['id'])
                data["test_user"]["devices"][project_id][idx]['enabled'] = True
        return jsonify({
            "changed": changed,
            "errors": {}
        })
    except KeyError:
        return jsonify({

            "errors": {
                "e": "datastore: key not found",
                "code": 400,
            }
        })


# DELETE(  /project/{projectid}/devices  )
@app.route("/project/<project_id>/devices", methods=["DELETE", "OPTIONS"])
def project_devices_delete(project_id):
    if request.method == "OPTIONS":
        return "", 200
    ids = request.json['ids']
    try:
        devices = data["test_user"]["devices"][project_id]
        deleted = []
        for idx, device in enumerate(devices):
            if device['id'] in ids:
                deleted.append(device['id'])
                del data["test_user"]["devices"][project_id][idx]
        return jsonify({"deleted": deleted, "errors": {}})
    except KeyError as e:
        return jsonify({"deleted": [], "errors": {"e": e}})


@app.route("/clear", methods=["GET", "OPTIONS"])
def clear():
    if request.method == "OPTIONS":
        return "", 200
    global data
    data = {
        "test_user": {
            "info": {
                "company": "",
                "email": "",
                "login": "test_user",
                "name": ""
            },
            "repositories": {},
            "projects": [],
            "devices": {},
            "domains": {},
        }
    }
    return jsonify(data)


if __name__ == "__main__":
    try:
        port = sys.argv[1]
    except:
        port = 8083
    print("Mock server starting on port", port)
    app.run(port=port)
